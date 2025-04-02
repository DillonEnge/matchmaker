package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

	lobbyCode, err := c.CreateLobby()
	if err != nil {
		slog.Error("failed to create lobby", "err", err)
		return err
	}

	fmt.Printf("press enter to start lobby with code: %s\n", lobbyCode)
	r := bufio.NewReader(os.Stdin)
	r.ReadBytes('\n')

	slog.Info("STARTING LOBBY")

	if err := c.StartLobby(lobbyCode); err != nil {
		slog.Error("failed to start lobby", "err", err)
	}

	slog.Info("WAITING")

	l, peers, err := c.Wait(context.Background())
	if err != nil {
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
