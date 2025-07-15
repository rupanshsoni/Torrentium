package db

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

var DB *pgxpool.Pool

func InitDB() {
    if err := godotenv.Load(); err != nil {
        log.Printf("Warning: No .env file found or could not load .env file: %v. Proceeding without .env variables.", err)
    }

    host := os.Getenv("DB_HOST")
    port := os.Getenv("DB_PORT")
    user := os.Getenv("DB_USER")
    password := os.Getenv("DB_PASSWORD")
    dbname := os.Getenv("DB_NAME")

    if user == "" || password == "" || host == "" || port == "" || dbname == "" {
        log.Fatal("Error: One or more database environment variables are not set (DB_USER, DB_PASSWORD, DB_HOST, DB_PORT, DB_NAME). Please ensure they are set in your environment or .env file.")
    }

    dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
        user, password, host, port, dbname)


    ctx := context.Background()
    var err error
    DB, err = pgxpool.New(ctx, dbURL)
    if err != nil {
        log.Fatalf("Error creating DB pool with URL '%s': %v\n", dbURL, err)
    }

    err = DB.Ping(ctx)
    if err != nil {
        log.Fatalf("Error connecting to DB at URL '%s': %v\n", dbURL, err) 
    }

    log.Println("-> Successfully connected to DB")
}