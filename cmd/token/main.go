package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Aryan-Jagadale/paytm-seat-reservation/internal/auth"
)

func main() {
	secret := os.Getenv("AUTH_JWT_SECRET")

	if secret == "" {
		log.Fatal("AUTH_JWT_SECRET is required")
	}

	if len(os.Args) != 3 {
		log.Fatal("usage: go run ./cmd/token <user-id> <role>")
	}

	userID := os.Args[1]
	role := os.Args[2]

	if role != "user" && role != "admin" {
		log.Fatal("role must be user or admin")
	}

	token, err := auth.GenerateToken(
		secret,
		userID,
		role,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(token)
}
