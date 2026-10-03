package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
)

type tokenRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type createShowRequest struct {
	Name         string   `json:"name"`
	Seats        []string `json:"seats"`
	PricePaise   int64    `json:"price_paise"`
	PerUserLimit int      `json:"per_user_limit"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`

	// Intentionally included to test spoofing.
	UserID string `json:"user_id"`
}

type reservationResponse struct {
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	ShowID  string `json:"show_id"`
	Status  string `json:"status"`
	Amount  int64  `json:"amount_paise"`
}

type showResponse struct {
	ID     string `json:"id"`
	Seats  []seatResponse `json:"seats"`
	Counts seatCounts     `json:"counts"`
}

type seatResponse struct {
	ID         string `json:"id"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
	UserID     string `json:"user_id"`
}

type seatCounts struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Held      int `json:"held"`
	Confirmed int `json:"confirmed"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/identity <BASE_URL>")
		fmt.Println("example: go run ./scripts/identity http://127.0.0.1:8081")
		os.Exit(1)
	}

	baseURL := trimTrailingSlash(os.Args[1])
	client := newHTTPClient()

	const (
		userA = "identity-user-A"
		userB = "identity-user-B"
		seat  = "IDENTITY-1"
	)

	fmt.Println("========================================")
	fmt.Println("       IDENTITY / SPOOFING TEST")
	fmt.Println("========================================")
	fmt.Printf("Base URL : %s\n", baseURL)
	fmt.Printf("User A   : %s\n", userA)
	fmt.Printf("User B   : %s\n", userB)
	fmt.Printf("Seat     : %s\n", seat)
	fmt.Println()

	// --------------------------------------------------
	// 1. Generate admin token
	// --------------------------------------------------

	fmt.Println("[1/7] Generating admin token...")

	adminToken, err := generateToken(
		client,
		baseURL,
		"identity-admin",
		"admin",
	)
	if err != nil {
		fatal("admin token failed", err)
	}

	// --------------------------------------------------
	// 2. Create fresh show
	// --------------------------------------------------

	fmt.Println("[2/7] Creating fresh show...")

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		seat,
	)
	if err != nil {
		fatal("create show failed", err)
	}

	fmt.Printf("      Show ID: %s\n", showID)

	// --------------------------------------------------
	// 3. Generate User A and User B tokens
	// --------------------------------------------------

	fmt.Println("[3/7] Generating user tokens...")

	tokenA, err := generateToken(
		client,
		baseURL,
		userA,
		"user",
	)
	if err != nil {
		fatal("user A token failed", err)
	}

	tokenB, err := generateToken(
		client,
		baseURL,
		userB,
		"user",
	)
	if err != nil {
		fatal("user B token failed", err)
	}

	// --------------------------------------------------
	// 4. User A reserves while spoofing User B in body
	// --------------------------------------------------

	fmt.Println("[4/7] User A reserves while spoofing User B...")

	reservation, err := reserve(
		client,
		baseURL,
		showID,
		tokenA,
		seat,
		"identity-test-key",
		userB, // <-- malicious body value
	)
	if err != nil {
		fatal("spoofed reservation failed", err)
	}

	fmt.Printf("      Reservation ID : %s\n", reservation.ID)
	fmt.Printf("      Token identity : %s\n", userA)
	fmt.Printf("      Body user_id   : %s\n", userB)
	fmt.Printf("      Stored user_id : %s\n", reservation.UserID)
	fmt.Printf("      Status         : %s\n", reservation.Status)

	if reservation.UserID != userA {
		fmt.Println()
		fmt.Println("FAIL: reservation identity came from request body")
		os.Exit(1)
	}

	fmt.Println("      PASS: token identity wins over spoofed body")

	// --------------------------------------------------
	// 5. User B tries to cancel User A's reservation
	// --------------------------------------------------

	fmt.Println()
	fmt.Println("[5/7] User B attempts to cancel User A's reservation...")

	status, body, err := cancelReservation(
		client,
		baseURL,
		reservation.ID,
		tokenB,
	)

	if err != nil {
		fatal("cancel request failed", err)
	}

	fmt.Printf("      HTTP %d -> %s\n", status, body)

	if status != http.StatusForbidden {
		fmt.Printf(
			"FAIL: expected HTTP 403, got HTTP %d\n",
			status,
		)
		os.Exit(1)
	}

	fmt.Println("      PASS: User B cannot cancel User A's reservation")

	// --------------------------------------------------
	// 6. User A cancels its own reservation
	// --------------------------------------------------

	fmt.Println()
	fmt.Println("[6/7] User A cancels its own reservation...")

	status, body, err = cancelReservation(
		client,
		baseURL,
		reservation.ID,
		tokenA,
	)

	if err != nil {
		fatal("own cancel request failed", err)
	}

	fmt.Printf("      HTTP %d -> %s\n", status, body)

	if status < 200 || status >= 300 {
		fmt.Printf(
			"FAIL: User A should be able to cancel its own reservation, got HTTP %d\n",
			status,
		)
		os.Exit(1)
	}

	fmt.Println("      PASS: User A can cancel its own reservation")

	// --------------------------------------------------
	// 7. Verify final seat state
	// --------------------------------------------------

	fmt.Println()
	fmt.Println("[7/7] Verifying final seat state...")

	show, err := getShow(
		client,
		baseURL,
		showID,
	)
	if err != nil {
		fatal("failed to fetch final show", err)
	}

	fmt.Printf("      Total     : %d\n", show.Counts.Total)
	fmt.Printf("      Available : %d\n", show.Counts.Available)
	fmt.Printf("      Held      : %d\n", show.Counts.Held)
	fmt.Printf("      Confirmed : %d\n", show.Counts.Confirmed)

	if show.Counts.Total !=
		show.Counts.Available+
			show.Counts.Held+
			show.Counts.Confirmed {

		fmt.Println()
		fmt.Println("FAIL: reconciliation invariant violated")
		os.Exit(1)
	}

	if show.Counts.Available != 1 ||
		show.Counts.Confirmed != 0 ||
		show.Counts.Held != 0 {

		fmt.Println()
		fmt.Println("FAIL: cancelled seat was not released correctly")
		os.Exit(1)
	}

	fmt.Println("      PASS: seat returned to available")

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       IDENTITY TEST PASSED")
	fmt.Println("========================================")
}

func newHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil,

		DialContext: (&net.Dialer{}).DialContext,

		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 20,
		MaxConnsPerHost:     20,

		DisableKeepAlives: false,
	}

	return &http.Client{
		Transport: transport,
	}
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

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

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

	payload, err := json.Marshal(createShowRequest{
		Name:         "Identity Test Show",
		Seats:        []string{seat},
		PricePaise:   10000,
		PerUserLimit: 4,
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

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"Authorization",
		"Bearer "+adminToken,
	)

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
		return "", fmt.Errorf("show ID missing")
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
	spoofedUserID string,
) (*reservationResponse, error) {

	payload, err := json.Marshal(reserveRequest{
		Seats:          []string{seat},
		IdempotencyKey: idempotencyKey,

		// This field should be completely ignored by the server.
		UserID: spoofedUserID,
	})
	if err != nil {
		return nil, err
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
		return nil, err
	}

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"Authorization",
		"Bearer "+token,
	)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf(
			"reservation failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response reservationResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return &response, nil
}

func cancelReservation(
	client *http.Client,
	baseURL string,
	reservationID string,
	token string,
) (int, string, error) {

	url := fmt.Sprintf(
		"%s/reservations/%s/cancel",
		baseURL,
		reservationID,
	)

	req, err := http.NewRequest(
		http.MethodPost,
		url,
		nil,
	)
	if err != nil {
		return 0, "", err
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+token,
	)

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

func getShow(
	client *http.Client,
	baseURL string,
	showID string,
) (*showResponse, error) {

	url := fmt.Sprintf(
		"%s/shows/%s",
		baseURL,
		showID,
	)

	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"get show failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response showResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return &response, nil
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}

func fatal(message string, err error) {
	fmt.Printf(
		"ERROR: %s: %v\n",
		message,
		err,
	)

	os.Exit(1)
}