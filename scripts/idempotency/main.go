package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

type tokenRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type result struct {
	statusCode   int
	reservationID string
	body         string
	err          error
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/idempotency <BASE_URL>")
		fmt.Println("example: go run ./scripts/idempotency http://127.0.0.1:8081")
		os.Exit(1)
	}

	baseURL := trimTrailingSlash(os.Args[1])

	const (
		userID         = "idempotency-user"
		seat           = "A3"
		idempotencyKey = "same-key-123"
		concurrency    = 20
	)

	client := &http.Client{}

	fmt.Println("========================================")
	fmt.Println("       IDEMPOTENCY CONCURRENCY TEST")
	fmt.Println("========================================")
	fmt.Printf("Base URL       : %s\n", baseURL)
	fmt.Printf("User           : %s\n", userID)
	fmt.Printf("Seat           : %s\n", seat)
	fmt.Printf("Idempotency key: %s\n", idempotencyKey)
	fmt.Printf("Requests       : %d\n", concurrency)
	fmt.Println()

	// 1. Create a fresh show with A3.
	fmt.Println("[1/4] Creating fresh test show...")

	adminToken, err := generateToken(client, baseURL, "idempotency-admin", "admin")
	if err != nil {
		fatal("failed to generate admin token", err)
	}

	showID, err := createShow(client, baseURL, adminToken, seat)
	if err != nil {
		fatal("failed to create show", err)
	}

	fmt.Printf("      Show ID: %s\n", showID)

	// 2. Generate user token.
	fmt.Println("[2/4] Generating user token...")

	token, err := generateToken(client, baseURL, userID, "user")
	if err != nil {
		fatal("failed to generate user token", err)
	}

	// 3. Fire concurrent requests with SAME key.
	fmt.Println("[3/4] Firing concurrent requests with SAME idempotency key...")

	results := make(chan result, concurrency)

	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			status, body, reservationID, err := reserve(
				client,
				baseURL,
				showID,
				token,
				seat,
				idempotencyKey,
			)

			results <- result{
				statusCode:    status,
				reservationID: reservationID,
				body:          body,
				err:           err,
			}
		}()
	}

	wg.Wait()
	close(results)

	success := 0
	conflicts := 0
	errors := 0

	reservationIDs := make(map[string]int)

	for r := range results {
		if r.err != nil {
			errors++
			fmt.Println("ERROR:", r.err)
			continue
		}

		switch r.statusCode {
		case http.StatusCreated:
			success++

			if r.reservationID != "" {
				reservationIDs[r.reservationID]++
			}

		case http.StatusConflict:
			conflicts++

			fmt.Printf(
				"409 -> %s\n",
				r.body,
			)

		default:
			errors++

			fmt.Printf(
				"HTTP %d -> %s\n",
				r.statusCode,
				r.body,
			)
		}
	}

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("              SUMMARY")
	fmt.Println("========================================")

	fmt.Printf("201 Created     : %d\n", success)
	fmt.Printf("409 Conflict    : %d\n", conflicts)
	fmt.Printf("Errors          : %d\n", errors)
	fmt.Printf("Reservation IDs : %v\n", reservationIDs)

	// Same-key requests should all return the same reservation.
	if success != concurrency {
		fmt.Println()
		fmt.Printf(
			"FAIL: expected %d successful idempotent responses, got %d\n",
			concurrency,
			success,
		)
		os.Exit(1)
	}

	if errors != 0 {
		fmt.Println()
		fmt.Println("FAIL: errors detected")
		os.Exit(1)
	}

	if len(reservationIDs) != 1 {
		fmt.Println()
		fmt.Println("FAIL: multiple reservation IDs detected")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("PASS: all requests returned the same reservation")

	// 4. Same key + DIFFERENT seat must be 409.
	fmt.Println()
	fmt.Println("[4/4] Testing SAME key with DIFFERENT seat...")

	differentSeat := "A4"

	status, body, _, err := reserve(
		client,
		baseURL,
		showID,
		token,
		differentSeat,
		idempotencyKey,
	)

	if err != nil {
		fatal("different-seat idempotency request failed", err)
	}

	fmt.Printf("HTTP %d -> %s\n", status, body)

	if status != http.StatusConflict {
		fmt.Println()
		fmt.Println("FAIL: same key with different seats should return 409")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("PASS: same key + different seats returned 409")

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       IDEMPOTENCY TEST PASSED")
	fmt.Println("========================================")
}

func generateToken(
	client *http.Client,
	baseURL string,
	userID string,
	role string,
) (string, error) {
	payload, err := json.Marshal(tokenRequest{
		UserID: userID,
		Role:   role,
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"token request failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response tokenResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}

	if response.Token == "" {
		return "", fmt.Errorf("empty token returned")
	}

	return response.Token, nil
}

func createShow(
	client *http.Client,
	baseURL string,
	adminToken string,
	seat string,
) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"name":           "Idempotency Test Show",
		"seats":          []string{seat, "A4"},
		"price_paise":    10000,
		"per_user_limit": 4,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/shows",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf(
			"create show failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}

	if response.ID == "" {
		return "", fmt.Errorf("show ID missing from response")
	}

	return response.ID, nil
}

func reserve(
	client *http.Client,
	baseURL string,
	showID string,
	token string,
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
	req.Header.Set("Authorization", "Bearer "+token)

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

	if err := json.Unmarshal(body, &response); err != nil {
		// 409 responses won't necessarily contain an ID.
		// That's okay.
		if resp.StatusCode == http.StatusCreated {
			return resp.StatusCode, string(body), "", err
		}
	}

	return resp.StatusCode, string(body), response.ID, nil
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}

func fatal(message string, err error) {
	fmt.Printf("ERROR: %s: %v\n", message, err)
	os.Exit(1)
}