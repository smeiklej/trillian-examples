// Copyright 2021 Google LLC. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package impl is the implementation of the witness server.
package impl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang/glog"
	ih "github.com/google/trillian-examples/witness/golang/cmd/witness/internal/http"
	"github.com/google/trillian-examples/witness/golang/cmd/witness/internal/witness"
	"github.com/google/trillian/merkle/rfc6962/hasher"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3" // Load drivers for sqlite3
	"golang.org/x/mod/sumdb/note"
)

// LogConfig contains a list of LogInfo (configuration options for a log).
type LogConfig struct {
	Logs []LogInfo `json:"logs"`
}

// LogInfo contains the configuration options for a log: its identifier, hashing
// strategy, and public key.
type LogInfo struct {
	LogID        string `json:"logID"`
	HashStrategy string `json:"hashstrategy"`
	PubKey       string `json:"pubkey"`
	UseCompact   bool   `json:"usecompact"`
}

// The options for a server (specified in main.go).
type ServerOpts struct {
	// Where to listen for requests.
	ListenAddr string
	// The file for sqlite3 storage.
	DBFile string
	// The signer for the witness.
	Signer note.Signer
	// The log configuration information.
	Config LogConfig
}

// buildLogMap loads the log configuration information into a map.
func buildLogMap(config LogConfig) (map[string]witness.LogInfo, error) {
	logMap := make(map[string]witness.LogInfo)
	h := hasher.DefaultHasher
	for _, log := range config.Logs {
		// TODO(smeiklej): Extend witness to handle other hashing strategies.
		if log.HashStrategy != "default" {
			return nil, errors.New("can't handle non-default hashing strategies")
		}
		logV, err := note.NewVerifier(log.PubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create signature verifier: %v", err)
		}
		sigVs := []note.Verifier{logV}
		logInfo := witness.LogInfo{
			SigVs:      sigVs,
			Hasher:     h,
			UseCompact: log.UseCompact,
		}
		logMap[log.LogID] = logInfo
	}
	return logMap, nil
}

func Main(ctx context.Context, opts ServerOpts) error {
	if len(opts.DBFile) == 0 {
		return errors.New("DBFile is required")
	}
	// Start up local database.
	glog.Infof("Connecting to local DB at %q", opts.DBFile)
	db, err := sql.Open("sqlite3", opts.DBFile)
	if err != nil {
		return fmt.Errorf("failed to connect to DB: %w", err)
	}
	// Load log configuration into the map.
	logMap, err := buildLogMap(opts.Config)
	if err != nil {
		return fmt.Errorf("failed to load configurations: %v", err)
	}

	w, err := witness.New(witness.Opts{
		DB:        db,
		Signer:    opts.Signer,
		KnownLogs: logMap,
	})
	if err != nil {
		return fmt.Errorf("error creating witness: %v", err)
	}

	glog.Infof("Starting witness server...")
	srv := ih.NewServer(w)
	r := mux.NewRouter()
	srv.RegisterHandlers(r)
	hServer := &http.Server{
		Addr:    opts.ListenAddr,
		Handler: r,
	}
	e := make(chan error, 1)
	go func() {
		e <- hServer.ListenAndServe()
		close(e)
	}()
	<-ctx.Done()
	glog.Info("Server shutting down")
	hServer.Shutdown(ctx)
	return <-e
}
