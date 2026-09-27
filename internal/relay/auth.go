package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// HealthPath is the unauthenticated health check (Docs/protocol/relay-hosted.md §1).
const HealthPath = "/healthz"

// healthTimeout bounds the database probe behind HealthPath.
const healthTimeout = time.Second

// setAuth fixes which relay authentication versions the server offers.
func (s *Server) setAuth(opts Options) error {
	for _, raw := range opts.Origins {
		o, err := envelope.Origin(raw)
		if err != nil {
			return fmt.Errorf("relay origin %q: %w", raw, err)
		}
		if !slices.Contains(s.origins, o) {
			s.origins = append(s.origins, o)
		}
	}
	if opts.Public && len(s.origins) == 0 {
		return errors.New("a public relay needs at least one origin (--public-origin)")
	}
	s.authV1 = !opts.Public || opts.AllowAuthV1
	s.authV2 = len(s.origins) > 0
	return nil
}

// authOffer is the challenge's "auth" list.
func (s *Server) authOffer() []string {
	var offer []string
	if s.authV1 {
		offer = append(offer, envelope.AuthV1)
	}
	if s.authV2 {
		offer = append(offer, envelope.AuthV2)
	}
	return offer
}

// verifyAuth checks an auth frame of either version against the nonce this
// connection was challenged with. A valid v1 signature on a relay that
// requires v2 returns envelope.ErrAuthV1Refused.
func (s *Server) verifyAuth(c envelope.Control, nonce []byte) (ed25519.PublicKey, error) {
	switch c.V {
	case 2:
		if !s.authV2 {
			return nil, errors.New("auth v2 not offered")
		}
		return envelope.VerifyAuthV2(c, nonce, s.origins)
	case 0, 1:
		pub, err := envelope.VerifyAuth(c, nonce)
		if err != nil {
			return nil, err
		}
		if !s.authV1 {
			return nil, envelope.ErrAuthV1Refused
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("unknown auth version %d", c.V)
	}
}

// serveHealth answers 200 {"ok":true,"version":…} while the queue database
// answers a SELECT 1 within healthTimeout, else 503. It reveals no counts or keys.
func (s *Server) serveHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	var one int
	err := s.q.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil || one != 1 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version.Version})
}
