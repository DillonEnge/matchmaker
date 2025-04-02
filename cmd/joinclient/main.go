package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DillonEnge/matchmaker/client"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	c := client.NewClient("engehost.net:8070")

	if err := c.Connect(); err != nil {
		slog.Error("failed to connect to server", "err", err)
		return err
	}
	defer func() {
		tcpErr, udpErr := c.Close()
		if tcpErr != nil {
			slog.Error("error closing tcp conn", "err", tcpErr)
		}
		if udpErr != nil {
			slog.Error("error closing udp conn", "err", udpErr)
		}
	}()

	fmt.Printf("Enter lobby code: ")
	r := bufio.NewReader(os.Stdin)
	lobbyCode, err := r.ReadString('\n')
	if err != nil {
		slog.Error("failed to read lobby code from Stdin", "err", err)
		return err
	}

	err = c.JoinLobby(strings.TrimSpace(lobbyCode))
	if err != nil {
		slog.Error("failed to join lobby", "lobbyCode", strings.TrimSpace(lobbyCode))
		return err
	}

	l, peers, err := c.Wait(context.Background())
	if err != nil {
		slog.Error("failed to wait for peerConns", "err", err)
		return err
	}

	ctx := context.Background()
	ctx, _ = signal.NotifyContext(ctx, os.Interrupt)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			p := make([]byte, 1024)
			n, _, err := l.ReadFrom(p)
			if err != nil {
				slog.Error("failed to read from listener", "err", err)
				continue
			}

			slog.Info("received packet", "packet", string(p[:n]))
		}
	}()

	for _, p := range peers {
		slog.Info("peer conn received", "rAddr", p)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
					_, err := l.WriteTo([]byte("keep alive"), p)
					if err != nil {
						slog.Error("failed to send keep alive")
						continue
					}
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	}
}
