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

	if len(os.Args) != 2 {
		log.Fatal("usage: go run ./cmd/token <user-id>")
	}

	token, err := auth.GenerateToken(
		secret,
		os.Args[1],
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(token)
}