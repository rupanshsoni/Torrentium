package db

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func InitDB() {
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		user, password, host, port, dbname)

	ctx := context.Background()
	var err error
	DB, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("Error creating DB pool: ", err)
	}

	err = DB.Ping(ctx)
	if err != nil {
		log.Fatal("Error connecting to DB: ", err)
	}

	log.Println()
	fmt.Println("-> Successfully connected to DB")
}
