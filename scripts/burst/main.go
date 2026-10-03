package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	defaultConcurrency = 20000
	defaultWorkers     = 180
	defaultUsers       = 200

	pricePaise   = int64(10000)

	perUserLimit = 4

	maxNetworkErrorsToPrint = 10
)

var hotSeats = []string{
	"BURST-01",
	"BURST-02",
	"BURST-03",
	"BURST-04",
	"BURST-05",
	"BURST-06",
	"BURST-07",
	"BURST-08",
	"BURST-09",
	"BURST-10",
	"BURST-11",
	"BURST-12",
	"BURST-13",
	"BURST-14",
	"BURST-15",
	"BURST-16",
	"BURST-17",
	"BURST-18",
	"BURST-19",
	"BURST-20",
}

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

type createShowResponse struct {
	ID string `json:"id"`
}

type showResponse struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	PricePaise   int64       `json:"price_paise"`
	PerUserLimit int         `json:"per_user_limit"`
	Seats        []seatResult `json:"seats"`
	Counts       seatCounts  `json:"counts"`
}

type seatResult struct {
	ID         string `json:"id"`
	ShowID     string `json:"show_id"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
}

type seatCounts struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Held      int `json:"held"`
	Confirmed int `json:"confirmed"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`

	// Used only by the identity-spoof test.
	// The server must derive identity from JWT, not this field.
	UserID string `json:"user_id,omitempty"`
}

type reservationResponse struct {
	ID             string   `json:"id"`
	ShowID         string   `json:"show_id"`
	UserID         string   `json:"user_id"`
	Status         string   `json:"status"`
	AmountPaise    int64    `json:"amount_paise"`
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type burstResult struct {
	statusCode int
	reason     string
	reservation *reservationResponse
	err        error
}

type reservationAttempt struct {
	result burstResult
}

type reconciliationState struct {
	total     int
	available int
	held      int
	confirmed int
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/burst <BASE_URL>")
		fmt.Println("example: go run ./scripts/burst http://127.0.0.1:8081")
		os.Exit(1)
	}

	baseURL := trimTrailingSlash(os.Args[1])

	concurrency := getPositiveEnv(
		"CONCURRENCY",
		defaultConcurrency,
	)

	workers := getPositiveEnv(
		"WORKERS",
		defaultWorkers,
	)

	userCount := getPositiveEnv(
		"BURST_USERS",
		defaultUsers,
	)

	if workers > concurrency {
		workers = concurrency
	}

	if userCount > concurrency {
		userCount = concurrency
	}

	fmt.Println("========================================")
	fmt.Println("       SEAT RESERVATION ACCEPTANCE")
	fmt.Println("========================================")
	fmt.Printf("Base URL       : %s\n", baseURL)
	fmt.Printf("Hot seats      : %d\n", len(hotSeats))
	fmt.Printf("Burst requests : %d\n", concurrency)
	fmt.Printf("Workers        : %d\n", workers)
	fmt.Printf("Burst users    : %d\n", userCount)
	fmt.Printf("User limit     : %d\n", perUserLimit)
	fmt.Println()

	client := newHTTPClient()

	// ------------------------------------------------------------
	// Authentication
	// ------------------------------------------------------------

	fmt.Println("[1/8] Generating admin token...")

	adminToken, err := generateToken(
		client,
		baseURL,
		"acceptance-admin",
		"admin",
	)

	if err != nil {
		fatal("failed to generate admin token", err)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// User tokens
	// ------------------------------------------------------------

	fmt.Println("[2/8] Generating user tokens...")

	tokens, err := generateUserTokens(
		client,
		baseURL,
		userCount,
	)

	if err != nil {
		fatal("failed to generate user tokens", err)
	}

	fmt.Printf("      Generated %d tokens\n", len(tokens))

	// ------------------------------------------------------------
	// Main hot-seat storm
	// ------------------------------------------------------------

	fmt.Println("[3/8] Running hot-seat storm...")

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		"Hot Seat Burst",
		hotSeats,
		perUserLimit,
	)

	if err != nil {
		fatal("failed to create hot-seat show", err)
	}

	fmt.Printf("      Show ID: %s\n", showID)

	// Start reconciliation observer while the storm is running.
	observerStop := make(chan struct{})
	observerDone := make(chan error, 1)

	go observeReconciliation(
		client,
		baseURL,
		showID,
		observerStop,
		observerDone,
	)

	stormResult := runHotSeatStorm(
		client,
		baseURL,
		showID,
		tokens,
		concurrency,
		workers,
	)

	close(observerStop)

	observerErr := <-observerDone

	if observerErr != nil {
		fatal(
			"reconciliation invariant failed during burst",
			observerErr,
		)
	}

	printStormSummary(stormResult)

	if stormResult.networkErrors != 0 {
		fatal(
			"hot-seat storm produced network errors",
			fmt.Errorf("%d network errors", stormResult.networkErrors),
		)
	}

	if stormResult.fiveXX != 0 {
		fatal(
			"hot-seat storm produced 5xx responses",
			fmt.Errorf("%d 5xx responses", stormResult.fiveXX),
		)
	}

	if stormResult.confirmed != int64(len(hotSeats)) {
		fatal(
			"unexpected confirmed count",
			fmt.Errorf(
				"expected %d confirmed, got %d",
				len(hotSeats),
				stormResult.confirmed,
			),
		)
	}

	finalShow, err := getShow(
		client,
		baseURL,
		showID,
	)

	if err != nil {
		fatal("failed to fetch hot-seat final state", err)
	}

	if err := verifyReconciliation(finalShow); err != nil {
		fatal("final reconciliation failed", err)
	}

	if finalShow.Counts.Confirmed != len(hotSeats) {
		fatal(
			"hot-seat final confirmed count incorrect",
			fmt.Errorf(
				"expected %d, got %d",
				len(hotSeats),
				finalShow.Counts.Confirmed,
			),
		)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// Idempotency
	// ------------------------------------------------------------

	fmt.Println("[4/8] Testing idempotency...")

	if err := testIdempotency(
		client,
		baseURL,
		adminToken,
		tokens[0],
	); err != nil {
		fatal("idempotency test failed", err)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// Per-user limit
	// ------------------------------------------------------------

	fmt.Println("[5/8] Testing concurrent per-user limit...")

	if err := testPerUserLimit(
		client,
		baseURL,
		adminToken,
		tokens[0],
	); err != nil {
		fatal("per-user limit test failed", err)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// Identity
	// ------------------------------------------------------------

	fmt.Println("[6/8] Testing token-derived identity...")

	if len(tokens) < 2 {
		fatal(
			"identity test requires two users",
			errors.New("not enough tokens"),
		)
	}

	if err := testIdentity(
		client,
		baseURL,
		adminToken,
		tokens[0],
	); err != nil {
		fatal("identity test failed", err)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// Cancellation ownership
	// ------------------------------------------------------------

	fmt.Println("[7/8] Testing cancellation ownership...")

	if err := testCancellationOwnership(
		client,
		baseURL,
		adminToken,
		tokens[0],
		tokens[1],
	); err != nil {
		fatal("cancellation ownership test failed", err)
	}

	fmt.Println("      PASS")

	// ------------------------------------------------------------
	// Final output
	// ------------------------------------------------------------

	fmt.Println("[8/8] Acceptance checks complete")

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       ALL ACCEPTANCE TESTS PASSED")
	fmt.Println("========================================")
}

type stormSummary struct {
	confirmed       int64
	fiveXX          int64
	networkErrors   int64
	declinedByReason map[string]int64
	duration        time.Duration
}

func runHotSeatStorm(
	client *http.Client,
	baseURL string,
	showID string,
	tokens []string,
	requests int,
	workers int,
) stormSummary {

	jobs := make(chan int)
	results := make(chan burstResult, requests)

	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for requestID := range jobs {
				userIndex := requestID % len(tokens)

				userID := fmt.Sprintf(
					"burst-user-%d",
					userIndex+1,
				)

				token := tokens[userIndex]

				// Distribute requests across the handful of hot seats.
				seat := hotSeats[requestID%len(hotSeats)]

				result := reserveSeat(
					client,
					baseURL,
					showID,
					userID,
					token,
					seat,
					fmt.Sprintf(
						"burst-%s-%d",
						userID,
						requestID,
					),
				)

				results <- result
			}
		}()
	}

	go func() {
		for i := 0; i < requests; i++ {
			jobs <- i
		}

		close(jobs)

		wg.Wait()
		close(results)
	}()

	var summary stormSummary

	summary.declinedByReason = make(map[string]int64)

	for result := range results {
		if result.err != nil {
			summary.networkErrors++

			if summary.networkErrors <= maxNetworkErrorsToPrint {
				fmt.Printf(
					"NETWORK ERROR %d: %v\n",
					summary.networkErrors,
					result.err,
				)
			}

			continue
		}

		switch {
		case result.statusCode == http.StatusCreated:
			summary.confirmed++

		case result.statusCode >= 500:
			summary.fiveXX++

		case result.statusCode == http.StatusConflict:
			reason := result.reason

			if reason == "" {
				reason = "unknown_conflict"
			}

			summary.declinedByReason[reason]++

		default:
			reason := fmt.Sprintf(
				"http_%d",
				result.statusCode,
			)

			summary.declinedByReason[reason]++
		}
	}

	summary.duration = time.Since(start)

	return summary
}

func printStormSummary(summary stormSummary) {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("           HOT-SEAT STORM")
	fmt.Println("========================================")

	fmt.Printf(
		"Duration       : %s\n",
		summary.duration.Round(time.Millisecond),
	)

	fmt.Printf(
		"Confirmed      : %d\n",
		summary.confirmed,
	)

	fmt.Println("Declined:")

	for reason, count := range summary.declinedByReason {
		fmt.Printf(
			"  %-20s : %d\n",
			reason,
			count,
		)
	}

	fmt.Printf("5xx            : %d\n", summary.fiveXX)
	fmt.Printf("Network errors : %d\n", summary.networkErrors)

	fmt.Println("========================================")
}

func observeReconciliation(
	client *http.Client,
	baseURL string,
	showID string,
	stop <-chan struct{},
	done chan<- error,
) {

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			done <- nil
			return

		case <-ticker.C:
			show, err := getShow(
				client,
				baseURL,
				showID,
			)

			if err != nil {
				done <- fmt.Errorf(
					"observer failed: %w",
					err,
				)
				return
			}

			if err := verifyReconciliation(show); err != nil {
				done <- err
				return
			}
		}
	}
}

func verifyReconciliation(show *showResponse) error {
	sum :=
		show.Counts.Available +
			show.Counts.Held +
			show.Counts.Confirmed

	if sum != show.Counts.Total {
		return fmt.Errorf(
			"reconciliation invariant violated: available=%d held=%d confirmed=%d total=%d",
			show.Counts.Available,
			show.Counts.Held,
			show.Counts.Confirmed,
			show.Counts.Total,
		)
	}

	return nil
}

func testIdempotency(
	client *http.Client,
	baseURL string,
	adminToken string,
	userToken string,
) error {

	seats := []string{
		"IDEMP-1",
		"IDEMP-2",
	}

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		"Idempotency Test",
		seats,
		4,
	)

	if err != nil {
		return err
	}

	const key = "idempotency-same-key"

	var wg sync.WaitGroup
	results := make(chan burstResult, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results <- reserveSeat(
				client,
				baseURL,
				showID,
				"idempotency-user",
				userToken,
				"IDEMP-1",
				key,
			)
		}()
	}

	wg.Wait()
	close(results)

	var reservationID string
	successes := 0

	for result := range results {
		if result.err != nil {
			return result.err
		}

		if result.statusCode != http.StatusCreated {
			return fmt.Errorf(
				"same-key retry returned HTTP %d (%s)",
				result.statusCode,
				result.reason,
			)
		}

		if result.reservation == nil {
			return errors.New("missing reservation response")
		}

		successes++

		if reservationID == "" {
			reservationID = result.reservation.ID
		}

		if result.reservation.ID != reservationID {
			return fmt.Errorf(
				"idempotency created multiple reservations: %s != %s",
				reservationID,
				result.reservation.ID,
			)
		}
	}

	if successes != 10 {
		return fmt.Errorf(
			"expected 10 idempotent responses, got %d",
			successes,
		)
	}

	// Same key + different seat must conflict.
	conflict := reserveSeat(
		client,
		baseURL,
		showID,
		"idempotency-user",
		userToken,
		"IDEMP-2",
		key,
	)

	if conflict.err != nil {
		return conflict.err
	}

	if conflict.statusCode != http.StatusConflict {
		return fmt.Errorf(
			"same key + different seats returned HTTP %d",
			conflict.statusCode,
		)
	}

	if conflict.reason != "idempotency_key_reused_with_different_seats" {
		return fmt.Errorf(
			"unexpected idempotency conflict reason: %s",
			conflict.reason,
		)
	}

	show, err := getShow(
		client,
		baseURL,
		showID,
	)

	if err != nil {
		return err
	}

	if show.Counts.Confirmed != 1 {
		return fmt.Errorf(
			"idempotency created %d reservations",
			show.Counts.Confirmed,
		)
	}

	return verifyReconciliation(show)
}

func testPerUserLimit(
	client *http.Client,
	baseURL string,
	adminToken string,
	userToken string,
) error {

	seats := []string{
		"LIMIT-1",
		"LIMIT-2",
		"LIMIT-3",
		"LIMIT-4",
		"LIMIT-5",
		"LIMIT-6",
		"LIMIT-7",
		"LIMIT-8",
		"LIMIT-9",
		"LIMIT-10",
	}

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		"Per User Limit Test",
		seats,
		perUserLimit,
	)

	if err != nil {
		return err
	}

	results := make(chan burstResult, len(seats))

	var wg sync.WaitGroup

	for i, seat := range seats {
		wg.Add(1)

		go func(index int, seat string) {
			defer wg.Done()

			results <- reserveSeat(
				client,
				baseURL,
				showID,
				"limit-user",
				userToken,
				seat,
				fmt.Sprintf(
					"limit-user-key-%d",
					index,
				),
			)
		}(i, seat)
	}

	wg.Wait()
	close(results)

	var confirmed int
	var limitDeclined int

	for result := range results {
		if result.err != nil {
			return result.err
		}

		switch result.statusCode {
		case http.StatusCreated:
			confirmed++

		case http.StatusConflict:
			if result.reason == "per_user_limit" {
				limitDeclined++
			} else {
				return fmt.Errorf(
					"unexpected conflict reason: %s",
					result.reason,
				)
			}

		default:
			return fmt.Errorf(
				"unexpected response: HTTP %d",
				result.statusCode,
			)
		}
	}

	if confirmed > perUserLimit {
		return fmt.Errorf(
			"user obtained %d reservations with limit=%d",
			confirmed,
			perUserLimit,
		)
	}

	if confirmed != perUserLimit {
		return fmt.Errorf(
			"expected exactly %d successful reservations, got %d",
			perUserLimit,
			confirmed,
		)
	}

	if limitDeclined != len(seats)-perUserLimit {
		return fmt.Errorf(
			"expected %d per-user-limit declines, got %d",
			len(seats)-perUserLimit,
			limitDeclined,
		)
	}

	show, err := getShow(
		client,
		baseURL,
		showID,
	)

	if err != nil {
		return err
	}

	if show.Counts.Confirmed != perUserLimit {
		return fmt.Errorf(
			"show reports %d confirmed seats, expected %d",
			show.Counts.Confirmed,
			perUserLimit,
		)
	}

	return verifyReconciliation(show)
}

func testIdentity(
	client *http.Client,
	baseURL string,
	adminToken string,
	userAToken string,
) error {

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		"Identity Test",
		[]string{"IDENTITY-1"},
		4,
	)

	if err != nil {
		return err
	}

	result := reserveSeatWithSpoofedUser(
		client,
		baseURL,
		showID,
		userAToken,
		"real-user-a",
		"spoofed-user-b",
		"IDENTITY-1",
		"identity-test-key",
	)

	if result.err != nil {
		return result.err
	}

	if result.statusCode != http.StatusCreated {
		return fmt.Errorf(
			"identity test returned HTTP %d (%s)",
			result.statusCode,
			result.reason,
		)
	}

	if result.reservation == nil {
		return errors.New("identity response missing reservation")
	}

	if result.reservation.UserID != "real-user-a" {
		return fmt.Errorf(
			"identity spoof succeeded: reservation belongs to %q",
			result.reservation.UserID,
		)
	}

	return nil
}

func testCancellationOwnership(
	client *http.Client,
	baseURL string,
	adminToken string,
	userAToken string,
	userBToken string,
) error {

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		"Cancellation Ownership Test",
		[]string{"CANCEL-1"},
		4,
	)

	if err != nil {
		return err
	}

	reservation := reserveSeat(
		client,
		baseURL,
		showID,
		"cancel-user-a",
		userAToken,
		"CANCEL-1",
		"cancel-test-key",
	)

	if reservation.err != nil {
		return reservation.err
	}

	if reservation.statusCode != http.StatusCreated {
		return fmt.Errorf(
			"reservation creation returned HTTP %d",
			reservation.statusCode,
		)
	}

	if reservation.reservation == nil {
		return errors.New("missing reservation")
	}

	reservationID := reservation.reservation.ID

	// User B must not be able to cancel A's reservation.
	status, body, err := cancelReservation(
		client,
		baseURL,
		reservationID,
		userBToken,
	)

	if err != nil {
		return err
	}

	if status != http.StatusForbidden {
		return fmt.Errorf(
			"other user cancellation returned HTTP %d: %s",
			status,
			string(body),
		)
	}

	// User A can cancel its own reservation.
	status, body, err = cancelReservation(
		client,
		baseURL,
		reservationID,
		userAToken,
	)

	if err != nil {
		return err
	}

	if status != http.StatusOK {
		return fmt.Errorf(
			"owner cancellation returned HTTP %d: %s",
			status,
			string(body),
		)
	}

	return nil
}

func generateUserTokens(
	client *http.Client,
	baseURL string,
	userCount int,
) ([]string, error) {

	tokens := make([]string, userCount)

	const tokenWorkers = 20

	jobs := make(chan int)
	errCh := make(chan error, userCount)

	var wg sync.WaitGroup

	workers := tokenWorkers

	if workers > userCount {
		workers = userCount
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for userIndex := range jobs {
				userID := fmt.Sprintf(
					"burst-user-%d",
					userIndex+1,
				)

				token, err := generateToken(
					client,
					baseURL,
					userID,
					"user",
				)

				if err != nil {
					errCh <- fmt.Errorf(
						"%s: %w",
						userID,
						err,
					)
					continue
				}

				tokens[userIndex] = token
			}
		}()
	}

	go func() {
		for i := 0; i < userCount; i++ {
			jobs <- i
		}

		close(jobs)
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		return nil, err
	}

	return tokens, nil
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

	resp, err := postJSON(
		client,
		baseURL+"/dev/token",
		"",
		payload,
	)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "",
			fmt.Errorf(
				"token endpoint returned HTTP %d: %s",
				resp.StatusCode,
				string(body),
			)
	}

	var response tokenResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return "",
			fmt.Errorf(
				"decode token response: %w",
				err,
			)
	}

	if response.Token == "" {
		return "",
			errors.New("token endpoint returned empty token")
	}

	return response.Token, nil
}

func createShow(
	client *http.Client,
	baseURL string,
	adminToken string,
	name string,
	seats []string,
	limit int,
) (string, error) {

	payload, err := json.Marshal(
		createShowRequest{
			Name:         name,
			Seats:        seats,
			PricePaise:   pricePaise,
			PerUserLimit: limit,
		},
	)

	if err != nil {
		return "", err
	}

	resp, err := postJSON(
		client,
		baseURL+"/shows",
		adminToken,
		payload,
	)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated {
		return "",
			fmt.Errorf(
				"create show returned HTTP %d: %s",
				resp.StatusCode,
				string(body),
			)
	}

	var response createShowResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return "",
			fmt.Errorf(
				"decode show response: %w",
				err,
			)
	}

	if response.ID == "" {
		return "",
			errors.New("create show returned empty ID")
	}

	return response.ID, nil
}

func reserveSeat(
	client *http.Client,
	baseURL string,
	showID string,
	userID string,
	token string,
	seat string,
	idempotencyKey string,
) burstResult {

	return reserveSeatWithSpoofedUser(
		client,
		baseURL,
		showID,
		token,
		userID,
		"",
		seat,
		idempotencyKey,
	)
}

func reserveSeatWithSpoofedUser(
	client *http.Client,
	baseURL string,
	showID string,
	token string,
	actualUserID string,
	spoofedUserID string,
	seat string,
	idempotencyKey string,
) burstResult {

	request := reserveRequest{
		Seats:          []string{seat},
		IdempotencyKey: idempotencyKey,
	}

	if spoofedUserID != "" {
		request.UserID = spoofedUserID
	}

	payload, err := json.Marshal(request)

	if err != nil {
		return burstResult{
			err: err,
		}
	}

	url := fmt.Sprintf(
		"%s/shows/%s/reserve",
		baseURL,
		showID,
	)

	resp, err := postJSON(
		client,
		url,
		token,
		payload,
	)

	if err != nil {
		return burstResult{
			err: fmt.Errorf(
				"user=%s seat=%s: %w",
				actualUserID,
				seat,
				err,
			),
		}
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return burstResult{
			statusCode: resp.StatusCode,
			err: err,
		}
	}

	var errorResponse struct {
		Error string `json:"error"`
	}

	_ = json.Unmarshal(body, &errorResponse)

	result := burstResult{
		statusCode: resp.StatusCode,
		reason:     errorResponse.Error,
	}

	if resp.StatusCode == http.StatusCreated {
		var reservation reservationResponse

		if err := json.Unmarshal(body, &reservation); err != nil {
			return burstResult{
				statusCode: resp.StatusCode,
				err: fmt.Errorf(
					"decode reservation response: %w",
					err,
				),
			}
		}

		result.reservation = &reservation
	}

	return result
}

func cancelReservation(
	client *http.Client,
	baseURL string,
	reservationID string,
	token string,
) (int, []byte, error) {

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf(
			"%s/reservations/%s/cancel",
			baseURL,
			reservationID,
		),
		nil,
	)

	if err != nil {
		return 0, nil, err
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+token,
	)

	resp, err := client.Do(req)

	if err != nil {
		return 0, nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return resp.StatusCode, nil, err
	}

	return resp.StatusCode, body, nil
}

func getShow(
	client *http.Client,
	baseURL string,
	showID string,
) (*showResponse, error) {

	req, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/shows/%s",
			baseURL,
			showID,
		),
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
		return nil,
			fmt.Errorf(
				"get show returned HTTP %d: %s",
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

func postJSON(
	client *http.Client,
	url string,
	token string,
	payload []byte,
) (*http.Response, error) {

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

	if token != "" {
		req.Header.Set(
			"Authorization",
			"Bearer "+token,
		)
	}

	return client.Do(req)
}

func newHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        300,
		MaxIdleConnsPerHost: 200,
		MaxConnsPerHost:     200,

		IdleConnTimeout:       90 * time.Second,
		DisableKeepAlives:     false,
		ResponseHeaderTimeout: 90 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}
}

func getPositiveEnv(name string, fallback int) int {
	value := os.Getenv(name)

	if value == "" {
		return fallback
	}

	number, err := strconv.Atoi(value)

	if err != nil || number <= 0 {
		fmt.Printf(
			"invalid %s=%q, using %d\n",
			name,
			value,
			fallback,
		)

		return fallback
	}

	return number
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 &&
		value[len(value)-1] == '/' {

		value = value[:len(value)-1]
	}

	return value
}

func fatal(message string, err error) {
	fmt.Printf(
		"\nFAIL: %s: %v\n",
		message,
		err,
	)

	os.Exit(1)
}