package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping DB: %v", err)
	}

	// Check Users
	var userCount int
	err = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	if err != nil {
		log.Fatalf("Failed to count users: %v", err)
	}

	fmt.Printf("Current User Count: %d\n", userCount)

	if userCount == 0 {
		fmt.Println("Seeding Users...")
		_, err := db.Exec(`
			INSERT INTO users (name, email, password_hash, roles)
			VALUES 
			('Admin User', 'admin@toko.com', crypt('admin123', gen_salt('bf')), ARRAY['admin']),
			('Customer User', 'customer@toko.com', crypt('customer123', gen_salt('bf')), ARRAY['customer']);
		`)
		if err != nil {
			log.Fatalf("Failed to seed users: %v", err)
		}
		fmt.Println("Users seeded successfully.")
	} else {
		fmt.Println("Users already exist. Skipping seed.")
	}

	// Check Products
	var productCount int
	err = db.QueryRow("SELECT COUNT(*) FROM products").Scan(&productCount)
	if err != nil {
		log.Fatalf("Failed to count products: %v", err)
	}

	fmt.Printf("Current Product Count: %d\n", productCount)

	if productCount == 0 {
		fmt.Println("Warning: No products found. Migration 000007 should have seeded them.")
	} else {
		fmt.Println("Products exist.")
	}
}
