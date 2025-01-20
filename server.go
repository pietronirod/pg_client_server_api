package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Cotacao struct {
	Bid string `json:"bid"`
}

type CotacaoFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

type ApiCotacaoFetcher struct {
	url              string
	retry            int
	failureThreshold int
	failureCount     int
	circuitOpen      bool
	circuitMutex     sync.Mutex
	lastAttemptTime  time.Time
	circuitResetTime time.Duration
	fallbackValue    string
}

func NewApiCotacaoFetcher(url string, retry int, failureThreshold int, circuitResetTime time.Duration, fallbackValue string) CotacaoFetcher {
	return &ApiCotacaoFetcher{
		url:              url,
		retry:            retry,
		failureThreshold: failureThreshold,
		circuitResetTime: circuitResetTime,
		fallbackValue:    fallbackValue,
	}
}

func (f *ApiCotacaoFetcher) Fetch(ctx context.Context) (string, error) {
	f.circuitMutex.Lock()
	if f.circuitOpen && time.Since(f.lastAttemptTime) < f.circuitResetTime {
		f.circuitMutex.Unlock()
		log.Println("Circuit breaker is open, using fallback value")
		return f.fallbackValue, nil
	} else if f.circuitOpen && time.Since(f.lastAttemptTime) >= f.circuitResetTime {
		log.Println("Circuit breaker reset after cooldown")
		f.circuitOpen = false
		f.failureCount = 0
	}
	f.circuitMutex.Unlock()

	var lastErr error
	for i := 0; i <= f.retry; i++ {
		req, err := http.NewRequestWithContext(ctx, "GET", f.url, nil)
		if err != nil {
			return "", err
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Fetch attempt %d failed with status: %v", i+1, resp.StatusCode)
			f.incrementFailureCount()
			continue
		}

		var result map[string]Cotacao
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			lastErr = err
			log.Printf("Fetch attempt %d failed during decoding: %v", i+1, err)
			f.incrementFailureCount()
			continue
		}

		f.resetCircuit()
		return result["USDBRL"].Bid, nil
	}

	f.lastAttemptTime = time.Now()
	log.Println("All fetch attempts failed, using fallback value")
	return f.fallbackValue, lastErr
}

func (f *ApiCotacaoFetcher) incrementFailureCount() {
	f.circuitMutex.Lock()
	defer f.circuitMutex.Unlock()
	f.failureCount++
	if f.failureCount >= f.failureThreshold {
		f.circuitOpen = true
		log.Printf("Circuit breaker opened after %d failures", f.failureCount)
	}
}

func (f *ApiCotacaoFetcher) resetCircuit() {
	f.circuitMutex.Lock()
	defer f.circuitMutex.Unlock()
	f.failureCount = 0
	f.circuitOpen = false
}

type CotacaoRepository interface {
	Save(ctx context.Context, bid string) error
}

type SQLiteCotacaoRepository struct {
	db *sql.DB
}

func NewSQLiteCotacaoRepository(db *sql.DB) CotacaoRepository {
	return &SQLiteCotacaoRepository{db: db}
}

func (r *SQLiteCotacaoRepository) Save(ctx context.Context, bid string) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO cotacao(bid) VALUES(?)", bid)
	return err
}

type Server struct {
	fetcher    CotacaoFetcher
	repository CotacaoRepository
}

func NewServer(fetcher CotacaoFetcher, repository CotacaoRepository) *Server {
	return &Server{fetcher: fetcher, repository: repository}
}

func (s *Server) cotacaoHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cotacao, err := s.fetcher.Fetch(ctx)
	if err != nil {
		log.Printf("Error fetching cotacao: %v", err)
		http.Error(w, "Failed to fetch cotacao", http.StatusInternalServerError)
		return
	}

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer dbCancel()

	if err := s.repository.Save(dbCtx, cotacao); err != nil {
		log.Printf("Error saving cotacao: %v", err)
		http.Error(w, "Failed to save cotacao", http.StatusInternalServerError)
		return
	}

	response := map[string]string{"Cotacao": cotacao}
	json.NewEncoder(w).Encode(response)
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "./cotacao.db")
	if err != nil {
		log.Fatal(err)
	}

	sqlStmt := `CREATE TABLE IF NOT EXISTS cotacao (id INTEGER PRIMARY KEY AUTOINCREMENT, bid TEXT, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP);`

	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Fatalf("%q: %s\n", err, sqlStmt)
	}

	return db
}

func main() {
	db := initDB()
	defer db.Close()

	fetcher := NewApiCotacaoFetcher("https://economia.awesomeapi.com.br/json/last/USD-BRL", 3, 2, 2*time.Second, "1.00")
	repository := NewSQLiteCotacaoRepository(db)
	server := NewServer(fetcher, repository)

	http.HandleFunc("/cotacao", server.cotacaoHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
