package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Jstarzz/ambulance-tracking/internal/store"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "rotate-password" {
		fmt.Fprintln(os.Stderr, "usage: adminctl rotate-password <username>")
		os.Exit(2)
	}

	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to read password from stdin")
		os.Exit(1)
	}
	password = strings.TrimSpace(password)
	if len(password) < 16 {
		fmt.Fprintln(os.Stderr, "password must be at least 16 characters")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	username := strings.TrimSpace(os.Args[2])
	if _, err := s.RotateUserPassword(ctx, username, password); err != nil {
		fmt.Fprintf(os.Stderr, "rotate password: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Password rotated for %s; existing dispatcher sessions revoked.\n", username)
}
