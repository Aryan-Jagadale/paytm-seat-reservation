package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const (
	baseURL = "http://localhost:8080"
	showID  = "94260550-9896-4678-8b5a-18f8db1a7c80"

	userID = "per-user-test"
)

var seats = []string{
	"B1", "B2", "B3", "B4", "B5",
	"B6", "B7", "B8", "B9", "B10",
}

type result struct {
	statusCode int
	body       string
	err        error
}

func main() {
	client := &http.Client{}

	token, err := generateToken(client)
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup
	results := make(chan result, len(seats))

	for i, seat := range seats {
		wg.Add(1)

		go func(index int, seat string) {
			defer wg.Done()

			status, body, err := reserve(
				client,
				token,
				seat,
				fmt.Sprintf("per-user-key-%d", index),
			)

			results <- result{
				statusCode: status,
				body:       body,
				err:        err,
			}
		}(i, seat)
	}

	wg.Wait()
	close(results)

	success := 0
	perUserLimit := 0
	errors := 0

	for result := range results {
		if result.err != nil {
			errors++
			fmt.Println("ERROR:", result.err)
			continue
		}

		fmt.Printf(
			"HTTP %d -> %s\n",
			result.statusCode,
			result.body,
		)

		switch result.statusCode {
		case http.StatusCreated:
			success++

		case http.StatusConflict:
			if result.body == `{"error":"per_user_limit"}` {
				perUserLimit++
			}

		default:
			errors++
		}
	}

	fmt.Println()
	fmt.Println("========== SUMMARY ==========")
	fmt.Printf("201 Created       : %d\n", success)
	fmt.Printf("per_user_limit 409: %d\n", perUserLimit)
	fmt.Printf("Errors            : %d\n", errors)
	fmt.Println("=============================")
}

func generateToken(client *http.Client) (string, error) {
	payload := []byte(`{"user_id":"per-user-test"}`)

	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/dev/token",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf(
			"token request failed: %d %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	return response.Token, nil
}

func reserve(
	client *http.Client,
	token string,
	seat string,
	idempotencyKey string,
) (int, string, error) {

	payload, err := json.Marshal(map[string]any{
		"seats":           []string{seat},
		"idempotency_key": idempotencyKey,
	})
	if err != nil {
		return 0, "", err
	}

	url := fmt.Sprintf(
		"%s/shows/%s/reserve",
		baseURL,
		showID,
	)

	req, err := http.NewRequest(
		http.MethodPost,
		url,
		bytes.NewReader(payload),
	)
	if err != nil {
		return 0, "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}

	return resp.StatusCode, string(body), nil
}