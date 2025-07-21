// torrentfile/torrentfile.go
package torrentfile

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"

	bencode "github.com/jackpal/bencode-go"
)

type TorrentMeta struct {
	Filename  string    `bencode:"filename"`
	Length    int64     `bencode:"length"`
	Hash      string    `bencode:"hash"`
	CreatedAt time.Time `bencode:"created_at"`
}

func CreateTorrentFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	hashCalc := sha256.New()
	if _, err = io.Copy(hashCalc, file); err != nil {
		return err
	}
	hexHash := hex.EncodeToString(hashCalc.Sum(nil))

	meta := TorrentMeta{
		Filename:  info.Name(),
		Length:    info.Size(),
		Hash:      hexHash,
		CreatedAt: time.Now().UTC(),
	}

	outputName := filename + ".torrent"
	out, err := os.Create(outputName)
	if err != nil {
		return err
	}
	defer out.Close()

	return bencode.Marshal(out, meta)
}