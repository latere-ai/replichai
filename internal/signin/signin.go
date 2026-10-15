// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package signin keeps each person's Latere sign-in, so the server can act
// for them on the Latere platform while a session they started runs, long
// after the browser request that started it has ended (spec 048). It stores
// the person's refresh token sealed, rotates it under a lock every process
// honors, and mints from it the minutes-long token the platform's API takes.
// A token never leaves this package except toward Auth and the platform.
package signin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrNoSignIn is a person whose sign-in the server does not hold: they signed
// in before the server kept sign-ins, signed out, or Auth ended the sign-in.
// They have to sign in to ReplicHAI again.
var ErrNoSignIn = errors.New("ReplicHAI holds no Latere sign-in for this person; sign in to ReplicHAI again")

// Store keeps one sealed refresh token per person.
type Store interface {
	// Put records the person's refresh token, replacing any earlier one.
	Put(ctx context.Context, subject, refresh string) error
	// Rotate calls next with the person's current refresh token, under a
	// lock every process honors, and stores what it returns in its place.
	// A refresh token is spent once, so two processes refreshing one
	// person's sign-in at once must take turns. ErrNoSignIn when none is
	// held; an error from next stores nothing.
	Rotate(ctx context.Context, subject string, next func(current string) (string, error)) error
	// Forget removes the person's sign-in.
	Forget(ctx context.Context, subject string) error
}

// Sealer encrypts a token at rest with AES-GCM.
type Sealer struct{ aead cipher.AEAD }

// NewSealer reads a key of 32 bytes given as 64 hex characters.
func NewSealer(hexKey string) (*Sealer, error) {
	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(key) != 32 {
		return nil, errors.New("the sign-in key must be 32 bytes as 64 hex characters")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plain, binding it to the person it belongs to, so a sealed
// token copied onto another person's row does not open.
func (s *Sealer) Seal(subject, plain string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal a sign-in: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), []byte(subject)), nil
}

// Open decrypts what Seal produced for the same person.
func (s *Sealer) Open(subject string, sealed []byte) (string, error) {
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("open a sign-in: too short")
	}
	plain, err := s.aead.Open(nil, sealed[:size], sealed[size:], []byte(subject))
	if err != nil {
		return "", errors.New("open a sign-in: it does not open with this key for this person")
	}
	return string(plain), nil
}

// MemStore is the in-memory Store, for a server with no database.
type MemStore struct {
	sealer *Sealer
	mu     sync.Mutex
	rows   map[string][]byte
	locks  map[string]*sync.Mutex
}

// NewMemStore builds an empty in-memory store.
func NewMemStore(sealer *Sealer) *MemStore {
	return &MemStore{sealer: sealer, rows: map[string][]byte{}, locks: map[string]*sync.Mutex{}}
}

// Put records the person's refresh token.
func (m *MemStore) Put(_ context.Context, subject, refresh string) error {
	sealed, err := m.sealer.Seal(subject, refresh)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[subject] = sealed
	return nil
}

func (m *MemStore) lock(subject string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, ok := m.locks[subject]
	if !ok {
		lock = &sync.Mutex{}
		m.locks[subject] = lock
	}
	return lock
}

// Rotate replaces the person's refresh token with what next returns.
func (m *MemStore) Rotate(_ context.Context, subject string, next func(string) (string, error)) error {
	lock := m.lock(subject)
	lock.Lock()
	defer lock.Unlock()
	m.mu.Lock()
	sealed, ok := m.rows[subject]
	m.mu.Unlock()
	if !ok {
		return ErrNoSignIn
	}
	current, err := m.sealer.Open(subject, sealed)
	if err != nil {
		return err
	}
	replacement, err := next(current)
	if err != nil {
		return err
	}
	resealed, err := m.sealer.Seal(subject, replacement)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[subject] = resealed
	return nil
}

// Forget removes the person's sign-in.
func (m *MemStore) Forget(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, subject)
	return nil
}
