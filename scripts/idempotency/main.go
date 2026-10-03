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
	statusCode int
	reservation string
	body       string
	err        error
}

func main() {
	const (
		userID          = "idempotency-user"
		seat            = "A3"
		idempotencyKey  = "same-key-123"
		concurrency     = 20
	)

	client := &http.Client{}

	token, err := generateToken(client, userID)
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	results := make(chan result, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			status, body, reservationID, err :=
				reserve(
					client,
					token,
					userID,
					seat,
					idempotencyKey,
				)

			results <- result{
				statusCode:  status,
				reservation: reservationID,
				body:        body,
				err:         err,
			}
		}()
	}

	wg.Wait()
	close(results)

	success := 0
	conflicts := 0
	errors := 0

	reservationIDs := make(map[string]int)

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

			reservationIDs[result.reservation]++

		case http.StatusConflict:
			conflicts++

		default:
			errors++
		}
	}

	fmt.Println()
	fmt.Println("========== SUMMARY ==========")
	fmt.Printf("201 Created : %d\n", success)
	fmt.Printf("409 Conflict: %d\n", conflicts)
	fmt.Printf("Errors      : %d\n", errors)
	fmt.Printf("Reservation IDs: %v\n", reservationIDs)
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
			"token request failed: %d %s",
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

func reserve(
	client *http.Client,
	token string,
	userID string,
	seat string,
	idempotencyKey string,
) (int, string, string, error) {
	payload, err := json.Marshal(reserveRequest{
		Seats:          []string{seat},
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return 0, "", "", err
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
		return 0, "", "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(
		"Authorization",
		"Bearer "+token,
	)

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", "", err
	}

	var response struct {
		ID string `json:"id"`
	}

	if resp.StatusCode == http.StatusCreated {
		if err := json.Unmarshal(body, &response); err != nil {
			return resp.StatusCode, string(body), "", err
		}
	}

	return resp.StatusCode, string(body), response.ID, nil
}