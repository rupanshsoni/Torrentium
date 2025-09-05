# Torrentium

**Decentralized P2P File Sharing System built in Go**

Torrentium is a fully decentralized peer-to-peer file sharing client. It uses libp2p's Distributed Hash Table (DHT) for peer discovery and WebRTC for direct file transfers, eliminating the need for centralized trackers.

## Features

- **Decentralized peer discovery** (libp2p Kademlia DHT)
- **Direct file transfer** via WebRTC data channels
- **Content-addressable storage** (IPFS CIDs)
- **Chunked file transfer** with resume capability
- **SHA-256 integrity verification**
- **SQLite-based local database** for metadata and history
- **Peer reputation scoring**
- **CLI interface** for file sharing and management

## Quick Start

### Prerequisites

- Go 1.23+
- Internet connection

### Installation

```bash
git clone https://github.com/ArunCS1005/Torrentium.git
cd Torrentium
go mod tidy
go build -o torrentium ./cmd/client/
```

### Usage

```bash
./torrentium
```

#### Example CLI Commands

- `add /path/to/file.txt` — Share a file
- `list` — List shared files
- `search "filename"` — Search local index
- `search <CID>` — Search by CID
- `download <CID>` — Download a file
- `peers` — List connected peers
- `health` — Show network health

## Architecture

- **Transport:** TCP/WebSocket (libp2p)
- **Discovery:** Kademlia DHT
- **Transfer:** WebRTC data channels
- **Storage:** SQLite database

```
Client App ── libp2p Host ── SQLite DB
      │           │             │
      ▼           ▼             ▼
      └───── WebRTC Data Channels ─────┘
```

## Database Schema

- `local_files`: Shared file metadata
- `downloads`: Download history
- `pieces`: Chunk-level tracking
- `peer_scores`: Peer reputation

## Contributing

1. Fork the repo
2. Create a feature branch
3. Commit and push your changes
4. Open a Pull Request

## License

MIT License. See [LICENSE](LICENSE).

## Support

- [GitHub Issues](https://github.com/ArunCS1005/Torrentium/issues)
- [Discussions](https://github.com/ArunCS1005/Torrentium/discussions)

---

Empowering decentralized file sharing for the modern web. 4. **Run the client**:

```bash
./torrentium
```

### Configuration

Create a `.env` file in the project root for custom configuration:

```env
SQLITE_DB_PATH=./custom_peer.db
```

## 📖 Usage Guide

### Basic Commands

The CLI provides an interactive interface with the following commands:

#### File Sharing

```
> add /path/to/your/file.txt
✓ File 'file.txt' is now being shared
 CID: bafybeig...(generated hash)
 Hash: a1b2c3...
 Size: 1.2 MB
```

#### Listing Shared Files

```
> list
=== Your Shared Files ===
Name: file.txt
 CID: bafybeig...
 Size: 1.2 MB
 Path: /path/to/your/file.txt
```

#### Searching for Files

```
# Search by filename
> search "document"
Local index matches for 'document':
- document.pdf  CID:bafybeig...

# Search by exact CID
> search bafybeig...
Found 3 provider(s):
 1. 12D3KooW... - Connected
 2. 12D3KooW... - Not connected
```

#### Downloading Files

```
> download bafybeig...
Looking for providers of CID: bafybeig...
Found 2 provider(s). Attempting connections...
Downloading to bafybeig....download...
Download complete!
```

#### Network Management

```
# View connected peers
> peers
=== Connected Peers (5) ===
Peer: 12D3KooW...
 Address: /ip4/192.168.1.100/tcp/4001

# Connect to specific peer
> connect /ip4/127.0.0.1/tcp/54437/p2p/12D3KooW...

# Check network health
> health
=== Connection Health ===
Connected peers: 8
 - Good peer connectivity
DHT routing table size: 45
 - Good DHT connectivity
```

### Advanced Features

#### Manual File Announcement

```
> announce bafybeig...
Re-announcing CID bafybeig... to DHT...
 - Successfully announced to DHT
```

#### Debug Information

```
> debug
=== Network Debug Info ===
Our Peer ID: 12D3KooW...
Our Addresses:
 /ip4/192.168.1.100/tcp/4001/p2p/12D3KooW...
 /ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...

Connected Peers (8):
DHT Routing Table Size: 45
Shared Files (3):
```

## 🔧 Technical Deep Dive

### File Processing Pipeline

1. **File Addition**:

   - File is read and SHA-256 hash calculated
   - Content is chunked into 1MB pieces
   - Each piece hash is stored in SQLite
   - IPFS CID is generated using multihash
   - File metadata announced to DHT

2. **Peer Discovery**:

   - DHT lookup for content providers
   - Connection establishment via libp2p
   - WebRTC negotiation through signaling protocol
   - Peer reputation scoring

3. **Data Transfer**:
   - WebRTC data channel establishment
   - Chunked transfer with progress tracking
   - Real-time integrity verification
   - Resume capability for interrupted downloads

### Database Schema

The SQLite database maintains several key tables:

```sql
-- File metadata for shared content
CREATE TABLE local_files (
    id TEXT PRIMARY KEY,
    cid TEXT UNIQUE NOT NULL,
    filename TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    file_hash TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Download history and state
CREATE TABLE downloads (
    id TEXT PRIMARY KEY,
    cid TEXT UNIQUE NOT NULL,
    filename TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    download_path TEXT NOT NULL,
    downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT DEFAULT 'completed'
);

-- Piece-level tracking for resume capability
CREATE TABLE pieces (
    id TEXT PRIMARY KEY,
    cid TEXT NOT NULL,
    idx INTEGER NOT NULL,
    offset INTEGER NOT NULL,
    size INTEGER NOT NULL,
    hash TEXT NOT NULL,
    have INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (cid, idx)
);

-- Peer reputation system
CREATE TABLE peer_scores (
    peer_id TEXT PRIMARY KEY,
    score REAL NOT NULL,
    seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### WebRTC Integration

Torrentium uses WebRTC data channels for efficient peer-to-peer communication:

- **ICE servers**: Multiple STUN/TURN servers for NAT traversal
- **Signaling**: Custom libp2p protocol for WebRTC offer/answer exchange
- **Data transfer**: Binary data channels for file content
- **Control messages**: JSON messages for file requests and metadata

## 🔗 Dependencies

### Core Libraries

- **[go-libp2p](https://github.com/libp2p/go-libp2p)**: P2P networking framework
- **[go-libp2p-kad-dht](https://github.com/libp2p/go-libp2p-kad-dht)**: Kademlia DHT implementation
- **[pion/webrtc](https://github.com/pion/webrtc)**: WebRTC implementation in Go
- **[go-sqlite3](https://github.com/mattn/go-sqlite3)**: SQLite database driver

### Utility Libraries

- **[go-cid](https://github.com/ipfs/go-cid)**: Content Identifier implementation
- **[go-multihash](https://github.com/multiformats/go-multihash)**: Multihash support
- **[progressbar](https://github.com/schollz/progressbar)**: CLI progress visualization
- **[humanize](https://github.com/dustin/go-humanize)**: Human-readable file sizes

## 🛠️ Development

### Project Structure

```
torrentium/
├── cmd/client/           # Main client application
│   ├── main.go          # CLI interface and core logic
│   ├── peer.db          # SQLite database (generated)
│   └── private_key      # libp2p identity (generated)
├── internal/
│   ├── client/          # WebRTC client implementation
│   │   └── webrtc.go   # WebRTC peer management
│   ├── db/             # Database layer
│   │   └── db.go       # SQLite operations and schema
│   └── p2p/            # P2P networking
│       ├── host.go     # libp2p host creation and management
│       └── signaling.go # WebRTC signaling protocol
├── go.mod              # Go module definition
├── go.sum              # Dependency checksums
└── README.md           # This file
```

### Building and Testing

```bash
# Build the client
go build -o torrentium ./cmd/client/

# Run with debug output
go run ./cmd/client/

# Test the build
./torrentium
```

### Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 🔒 Security Considerations

- **Content Integrity**: All files verified using SHA-256 hashing
- **Peer Authentication**: libp2p cryptographic identities
- **NAT Traversal**: Secure STUN/TURN server usage
- **Local Storage**: SQLite database with appropriate file permissions
- **Network Security**: Encrypted WebRTC data channels

## 🗺️ Roadmap

- [ ] **Web Interface**: Browser-based GUI for easier usage
- [ ] **Mobile Support**: Android/iOS client applications
- [ ] **Improved Search**: Full-text search across file contents
- [ ] **Bandwidth Control**: Rate limiting and QoS features
- [ ] **Plugin System**: Extensible architecture for custom protocols
- [ ] **Analytics Dashboard**: Network statistics and performance metrics

## 🐛 Troubleshooting

### Common Issues

1. **No peers found**: Check internet connection and firewall settings
2. **Download failures**: Verify CID format and provider availability
3. **Database errors**: Ensure write permissions in application directory
4. **Connection timeouts**: Try restarting and allowing more time for bootstrapping

### Debug Mode

Use the `debug` command to get detailed network information and diagnose connectivity issues.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- **IPFS Project** for content-addressable storage concepts
- **libp2p Community** for the robust P2P networking stack
- **Pion WebRTC** for excellent Go WebRTC implementation
- **Go Community** for the excellent ecosystem and libraries

## 📞 Support

For issues, questions, or contributions:

- **GitHub Issues**: [Create an issue](https://github.com/devlup-labs/Torrentium)
- **Discussions**: [GitHub Discussions](https://github.com/devlup-labs/Torrentium/discussions)

---

**Torrentium** - Empowering decentralized file sharing for the modern web 🌐
