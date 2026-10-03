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
	showID  = "3543b889-ead9-4576-a958-0eebac3bf042"
	seat    = "A2"

	// Use the same secret configured in docker-compose.yml.
	jwtSecret = "local-development-secret-change-me"

	concurrency = 20
)

type tokenRequest struct {
	UserID string `json:"user_id"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type result struct {
	userID     string
	statusCode int
	body       string
	err        error
}

func main() {
	var wg sync.WaitGroup
	results := make(chan result, concurrency)

	client := &http.Client{}

	for i := 1; i <= concurrency; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			userID := fmt.Sprintf("burst-user-%d", i)

			token, err := generateToken(client, userID)
			if err != nil {
				results <- result{
					userID: userID,
					err:    err,
				}
				return
			}

			statusCode, body, err := reserveSeat(
				client,
				userID,
				token,
			)

			results <- result{
				userID:     userID,
				statusCode: statusCode,
				body:       body,
				err:        err,
			}
		}(i)
	}

	wg.Wait()
	close(results)

	success := 0
	conflict := 0
	errors := 0

	for result := range results {
		if result.err != nil {
			errors++

			fmt.Printf(
				"%s ERROR: %v\n",
				result.userID,
				result.err,
			)

			continue
		}

		switch result.statusCode {
		case http.StatusCreated:
			success++

		case http.StatusConflict:
			conflict++

		default:
			errors++
		}

		fmt.Printf(
			"%s -> HTTP %d -> %s\n",
			result.userID,
			result.statusCode,
			result.body,
		)
	}

	fmt.Println()
	fmt.Println("========== SUMMARY ==========")
	fmt.Printf("201 Created : %d\n", success)
	fmt.Printf("409 Conflict: %d\n", conflict)
	fmt.Printf("Errors      : %d\n", errors)
	fmt.Println("=============================")
}

func generateToken(
	client *http.Client,
	userID string,
) (string, error) {
	payload, err := json.Marshal(tokenRequest{
		UserID: userID,
	})
	if err != nil {
		return "", err
	}

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
			"token endpoint returned %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response tokenResponse

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	return response.Token, nil
}

func reserveSeat(
	client *http.Client,
	userID string,
	token string,
) (int, string, error) {
	payload, err := json.Marshal(reserveRequest{
		Seats: []string{seat},
		IdempotencyKey: fmt.Sprintf(
			"burst-%s-A2",
			userID,
		),
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

	return resp.StatusCode, string(body), nil;
}