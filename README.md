# Matchmaker

A lightweight, efficient peer-to-peer matchmaking service written in Go that facilitates direct connections between network clients.

## Overview

Matchmaker provides a robust solution for establishing peer-to-peer connections, typically used in multiplayer games or real-time communication applications. It handles the complex process of NAT traversal, allowing clients to connect directly to each other without requiring a persistent intermediary server once connections are established.

## Features

- **Lobby Management**: Create and join game lobbies
- **P2P Connection Facilitation**: Server-assisted NAT traversal for direct client connections
- **Hybrid Communication Protocols**:
  - TCP for reliable control messages
  - UDP for efficient real-time data exchange
- **Session Management**: Persistent session tracking for reliable connectivity
- **Thread-Safe Operations**: Concurrent connection handling with safety guarantees

## Architecture

```
┌─────────────┐          ┌─────────────┐
│             │          │             │
│   Client A  │◄────────►│ Matchmaker  │
│             │          │   Server    │
└─────────────┘          └──────┬──────┘
       ▲                        │
       │                        │
       │                        │
       │                        ▼
       │                 ┌─────────────┐
       │                 │             │
       └────────────────►│   Client B  │
        Direct P2P       │             │
        Connection       └─────────────┘
```

1. Clients connect to the matchmaker server via TCP
2. Server facilitates lobby creation and joining
3. Server assists with NAT traversal between clients
4. Peers establish direct UDP connections
5. Once connected, peers communicate directly without server mediation

## Components

- **Server**: Handles matchmaking, lobby management, and connection facilitation
- **Client**: Connects to server and other peers, manages sessions
- **Lobby**: Represents a group of clients who want to connect
- **Message**: Defines communication protocols for TCP and UDP
- **Peer**: Manages direct connections between clients
- **Safety**: Thread-safe data structures for concurrent operations

## Getting Started

### Prerequisites

- Go 1.23.0 or higher

### Installation

```bash
git clone https://github.com/DillonEnge/matchmaker.git
cd matchmaker
go build ./...
```

### Running the Server

```bash
go run cmd/server/main.go
```

### Creating a Client Lobby

```bash
go run cmd/createclient/main.go
```

### Joining a Client Lobby

```bash
go run cmd/joinclient/main.go
```

## Client Usage Example

```go
package main

import (
    "github.com/DillonEnge/matchmaker/client"
)

func main() {
    // Create a new client
    c := client.NewClient("localhost:8080")
    
    // Create a lobby
    lobbyID, err := c.CreateLobby()
    if err != nil {
        panic(err)
    }
    
    // Handle incoming connections
    c.HandleConnections()
}
```

## Acknowledgements

- [Go Programming Language](https://golang.org/)
- [NAT Traversal Techniques](https://en.wikipedia.org/wiki/NAT_traversal)
