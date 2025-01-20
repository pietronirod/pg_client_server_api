package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

type CotacaoResponse struct {
	Bid string `json:"cotacao"`
}

func main() {
	url := "http://localhost:8080/cotacao"

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Fatalf("Error creating the request: %v", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error doing request: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error: HTTP status %d", resp.StatusCode)
		return
	}

	var cotacao CotacaoResponse
	if err := json.NewDecoder(resp.Body).Decode(&cotacao); err != nil {
		log.Printf("Erro ao decodificar a resposta JSON: %v", err)
		return
	}

	fmt.Printf("Dolar price: %s\n", cotacao.Bid)

	if err := saveCotacaoToFile(cotacao.Bid); err != nil {
		log.Printf("Error saving dolar price on file: %v", err)
		return
	}

	log.Println("Dolar price saved successfully on cotacao.txt file")
}

func saveCotacaoToFile(cotacao string) error {
	content := fmt.Sprintf("Dólar: %s", cotacao)
	return ioutil.WriteFile("cotacao.txt", []byte(content), 0644)
}
