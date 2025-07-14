package webRTC

import (
	"fmt"
	"strings"
)


// ParseCommand parses commands received from peers.
// Format: "COMMAND:filename:filesize"
func ParseCommand(command string) (cmd, filename, filesize string) {
	parts := strings.Split(command, ":")
	if len(parts) < 1 {
		return "", "", ""
	}

	cmd = parts[0]

	if len(parts) >= 2 {
		filename = parts[1]
	}
	if len(parts) >= 3 {
		// Just pass it through as string — let caller parse
		filesize = parts[2]
	}

	return
}


// PrintInstructions shows how to use the application.
func PrintInstructions() {
	fmt.Println(`📖 How to use Torrentium:

🔸 STEP 1: Person A types 'offer' to create a connection offer
🔸 STEP 2: Person A shares the offer JSON with Person B
🔸 STEP 3: Person B types 'answer <offer_json>' to create an answer
🔸 STEP 4: Person B shares the answer JSON with Person A
🔸 STEP 5: Person A types 'complete <answer_json>' to finish connection
🔸 STEP 6: Both can now transfer files using 'download <filename>'

💡 Tips:
   • This works through firewalls and NAT without port forwarding!
   • Files will be saved with 'downloaded_' prefix
   • Connection is direct and encrypted`)
}

// FormatFileSize converts bytes to human-readable format.
func FormatFileSize(bytes int64) string {
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

// Close closes the WebRTC peer connection and any open file writer.
func (p *WebRTCPeer) Close() error {
	fmt.Println("Closing WebRTC peer connection...")
	if p.fileWriter != nil {
		p.fileWriter.Close()
		p.fileWriter = nil
	}
	if p.dataChannel != nil {
		p.dataChannel.Close()
	}
	if p.connection != nil {
		return p.connection.Close()
	}
	return nil
}
