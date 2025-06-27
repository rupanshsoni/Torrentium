package main

import (
	"fmt"
	"strings"
	"strconv"
)

// parseCommand parses commands received from peers
// Commands format: "COMMAND:param1:param2"
func parseCommand(command string) (cmd, filename string, filesize int64) {
	parts := strings.Split(command, ":")
	
	if len(parts) < 1 {
		return "", "", 0
	}
	
	cmd = parts[0]
	
	if len(parts) >= 2 {
		filename = parts[1]
	}
	
	if len(parts) >= 3 {
		if size, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			filesize = size
		}
	}
	
	return cmd, filename, filesize
}

// printInstructions shows how to use the application
func printInstructions() {
	fmt.Println("📖 How to use Torrentium:")
	fmt.Println()
	fmt.Println("🔸 STEP 1: Person A types 'offer' to create a connection offer")
	fmt.Println("🔸 STEP 2: Person A shares the offer JSON with Person B")
	fmt.Println("🔸 STEP 3: Person B types 'answer <offer_json>' to create an answer")
	fmt.Println("🔸 STEP 4: Person B shares the answer JSON with Person A")
	fmt.Println("🔸 STEP 5: Person A types 'complete <answer_json>' to finish connection")
	fmt.Println("🔸 STEP 6: Both can now transfer files using 'download <filename>'")
	fmt.Println()
	fmt.Println("💡 Tips:")
	fmt.Println("   • This works through firewalls and NAT without port forwarding!")
	fmt.Println("   • Files will be saved with 'downloaded_' prefix")
	fmt.Println("   • Connection is direct and encrypted")
	fmt.Println()
}

// formatFileSize formats bytes into human readable format
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
