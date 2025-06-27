package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	fmt.Println("🔥 Torrentium - P2P File Sharing")
	fmt.Println("==================================")
	fmt.Println("Direct peer-to-peer file sharing that works through firewalls!")
	fmt.Println()

	// Show instructions
	printInstructions()

	// Create WebRTC peer
	peer, err := NewWebRTCPeer()
	if err != nil {
		fmt.Printf("❌ Error creating WebRTC peer: %v\n", err)
		return
	}
	defer peer.Close()

	// Command line interface
	scanner := bufio.NewScanner(os.Stdin)

	for {		fmt.Println("\n📋 Available Commands:")
		fmt.Println("  offer              - Create connection offer (start here)")
		fmt.Println("  answer <offer>     - Answer connection offer")
		fmt.Println("  complete <answer>  - Complete connection with answer")
		fmt.Println("  download <file>    - Download file from peer")
		fmt.Println("  status             - Show connection status")
		fmt.Println("  help               - Show instructions again")
		fmt.Println("  exit               - Quit application")
		fmt.Print("\n> ")

		// Read user input
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Parse command
		parts := strings.Fields(input)
		cmd := parts[0]

		// Handle commands
		switch cmd {
		case "exit", "quit", "q":
			fmt.Println("👋 Goodbye!")
			return

		case "help", "instructions":
			printInstructions()

		case "status":
			if peer.IsConnected() {
				fmt.Println("✅ Status: Connected and ready to transfer files")
			} else {
				fmt.Println("⏳ Status: Not connected yet")
			}

		case "offer":
			handleOfferCommand(peer)

		case "answer":
			if len(parts) < 2 {
				fmt.Println("❌ Usage: answer <offer_json>")
				fmt.Println("💡 Copy and paste the entire offer JSON from the other person")
				continue
			}
			// Join all parts except the first one (the command)
			offerJSON := strings.Join(parts[1:], " ")
			handleAnswerCommand(peer, offerJSON)

		case "complete":
			if len(parts) < 2 {
				fmt.Println("❌ Usage: complete <answer_json>")
				fmt.Println("💡 Copy and paste the entire answer JSON from the other person")
				continue
			}
			// Join all parts except the first one (the command)
			answerJSON := strings.Join(parts[1:], " ")
			handleCompleteCommand(peer, answerJSON)
		case "download":
			if len(parts) != 2 {
				fmt.Println("❌ Usage: download <filename>")
				fmt.Println("💡 Example: download hello.txt")
				continue
			}
			filename := parts[1]
			handleDownloadCommand(peer, filename)

		default:
			fmt.Printf("❌ Unknown command: %s\n", cmd)
			fmt.Println("💡 Type 'help' to see available commands")
		}
	}
}

// handleOfferCommand creates and displays a WebRTC offer
func handleOfferCommand(peer *WebRTCPeer) {
	fmt.Println("🔄 Creating WebRTC offer...")

	offer, err := peer.CreateOffer()
	if err != nil {
		fmt.Printf("❌ Error creating offer: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println("✅ Offer created successfully!")
	fmt.Println("📋 Copy this entire offer and send it to the other person:")
	fmt.Println("╔" + strings.Repeat("═", 60) + "╗")
	fmt.Printf("║ %-58s ║\n", "OFFER (copy everything below this line)")
	fmt.Println("╠" + strings.Repeat("═", 60) + "╣")
	fmt.Println(offer)
	fmt.Println("╚" + strings.Repeat("═", 60) + "╝")
	fmt.Println()
	fmt.Println("⏳ Waiting for the other person to send you their answer...")
}

// handleAnswerCommand creates and displays a WebRTC answer
func handleAnswerCommand(peer *WebRTCPeer, offerJSON string) {
	fmt.Println("🔄 Processing offer and creating answer...")

	answer, err := peer.CreateAnswer(offerJSON)
	if err != nil {
		fmt.Printf("❌ Error creating answer: %v\n", err)
		fmt.Println("💡 Make sure you copied the complete offer JSON")
		return
	}

	fmt.Println()
	fmt.Println("✅ Answer created successfully!")
	fmt.Println("📋 Copy this entire answer and send it back to the first person:")
	fmt.Println("╔" + strings.Repeat("═", 60) + "╗")
	fmt.Printf("║ %-58s ║\n", "ANSWER (copy everything below this line)")
	fmt.Println("╠" + strings.Repeat("═", 60) + "╣")
	fmt.Println(answer)
	fmt.Println("╚" + strings.Repeat("═", 60) + "╝")
	fmt.Println()
	fmt.Println("⏳ Waiting for connection to establish...")

	// Wait for connection
	go func() {
		if err := peer.WaitForConnection(30 * time.Second); err != nil {
			fmt.Printf("❌ Connection timeout: %v\n", err)
		}
	}()
}

// handleCompleteCommand completes the WebRTC connection
func handleCompleteCommand(peer *WebRTCPeer, answerJSON string) {
	fmt.Println("🔄 Completing WebRTC connection...")

	err := peer.SetAnswer(answerJSON)
	if err != nil {
		fmt.Printf("❌ Error setting answer: %v\n", err)
		fmt.Println("💡 Make sure you copied the complete answer JSON")
		return
	}

	fmt.Println("⏳ Establishing connection...")

	// Wait for connection to be established
	err = peer.WaitForConnection(30 * time.Second)
	if err != nil {
		fmt.Printf("❌ Connection timeout: %v\n", err)
		fmt.Println("💡 Try creating a new offer/answer pair")
		return
	}

	fmt.Println("🎉 Connection established successfully!")
	fmt.Println("✅ You can now transfer files using the 'download' command")
}

// handleDownloadCommand requests a file from the connected peer
func handleDownloadCommand(peer *WebRTCPeer, filename string) {
	if !peer.IsConnected() {
		fmt.Println("❌ Not connected to any peer")
		fmt.Println("💡 Complete the connection setup first using offer/answer/complete")
		return
	}

	fmt.Printf("📥 Requesting file: %s\n", filename)
	err := peer.RequestFile(filename)
	if err != nil {
		fmt.Printf("❌ Error requesting file: %v\n", err)
		return
	}

	fmt.Println("⏳ File request sent. Waiting for peer to send the file...")
	fmt.Println("💡 The file will be saved with 'downloaded_' prefix when received")
}
