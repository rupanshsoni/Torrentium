package main

import (
	"context"
	"log"
	

	"torrentium/db"
	"torrentium/p2p"
	"torrentium/tracker"
	"github.com/joho/godotenv"
)

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Unable to access .env file")
	}
	db.InitDB()

	h,err := p2p.NewHost(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	t := tracker.NewTracker()
	log.Println("-> Tracker Initialized")

	p2p.RegisterProtocol(h,t)

	select{}
}
