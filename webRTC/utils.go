package webRTC

import (
	// "encoding/base64"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// ParseCommand parses commands received from peers.
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
		filesize = parts[2]
	}

	return
}

// PrintInstructions shows how to use the application.
func PrintInstructions() {
	fmt.Println(`📖 How to use Torrentium:

🔸 STEP 1: Both peers run the application, which registers their addresses with the tracker.
🔸 STEP 2: To connect, one peer types 'offer <target_libp2p_peer_id>'
           (e.g., 'offer Qm...ABCD')
           The application will find the target peer via the tracker and initiate WebRTC.
🔸 STEP 3: Once connected, both peers can transfer files.
           To share a file: 'addfile <filename>'
           To download a file: 'download <filename>'

💡 Tips:
    • This works through firewalls and NAT without port forwarding!
    • Files will be saved with 'downloaded_' prefix.
    • Connection is direct and encrypted.`)
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

// closes the WebRTC peer connection and any open file writer.
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

func CalculateFileHash(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", 0, fmt.Errorf("could not hash file: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("could not get file info: %w", err)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), fileInfo.Size(), nil
}
