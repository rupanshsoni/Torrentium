package torrentfile

import (
	"crypto/sha1"
	"io"
	"os"
	"time"

	bencode "github.com/jackpal/bencode-go"
)

type Torrentmeta struct {
	Filename   string    `bencode:"filename"`
	Length     int64     `bencode:"length"`
	Hash       string    `bencode:"Hash"`
	Created_at time.Time `bencode:"Created_at"`
}

func CreateTorrentfile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	hashcalc := sha1.New()
	_, err = io.Copy(hashcalc, file)
	if err != nil {
		return err
	}
	hashSum := hashcalc.Sum(nil)

	meta := Torrentmeta{
		Filename:   info.Name(),
		Length:     info.Size(),
		Hash:       (string)(hashSum),
		Created_at: time.Now().UTC(),
	}

	outputName := filename + ".torrent"

	out, err := os.Create(outputName)
	if err != nil {
		return err
	}
	defer out.Close()
	err = bencode.Marshal(out, meta) // convert into bit torrent file
	if err != nil {
		return err
	}
	return nil
}
