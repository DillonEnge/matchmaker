package main

import (
	"context"

	"github.com/DillonEnge/matchmaker/server"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	s := server.NewServer(":8070")

	if err := s.ListenAndServe(context.Background()); err != nil {
		return err
	}

	return nil
}
