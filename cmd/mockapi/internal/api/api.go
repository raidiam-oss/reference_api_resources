package api

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/google/uuid"
)

// Example scope constant for the sample endpoint.
const sampleReadScope = "sample.read"

// Handler wires the HTTP mux with all public routes.
//
// In the open source repo we intentionally do NOT create any DB clients
// or seed any mock data. Downstream users can plug in persistence as needed.
func Handler(l *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Liveness probe. No auth.
	mux.Handle("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RespondWithJson(r.Context(), l, w, map[string]string{
			"status": "ok",
		})
	}))

	// Protected sample endpoint.
	// Shows how to enforce scopes, read authorizer context, and return JSON.
	mux.Handle(
		"/sample/protected",
		interactionIDMiddleware( // enforces / echoes x-fapi-interaction-id
			requireScopes(sampleReadScope)(
				http.HandlerFunc(sampleProtectedHandle(l)),
			),
		),
	)

	return mux
}

// sampleProtectedHandle returns a generic payload proving the caller is authenticated
// and authorized for the required scope.
func sampleProtectedHandle(l *slog.Logger) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := callerInfoFromAuthorizer(r)
		scopes := scopesFromRequest(r) // helper below; guaranteed to have required scope already

		// read back the x-fapi-interaction-id that interactionIDMiddleware ensured
		interactionID := r.Header.Get("x-fapi-interaction-id")

		resp := SampleResponse{
			Message:           "Access granted to protected resource",
			Scopes:            scopes,
			Caller:            caller,
			FapiInteractionID: interactionID,
		}

		RespondWithJson(r.Context(), l, w, resp)
	}
}

// interactionIDMiddleware ensures every request has a valid x-fapi-interaction-id.
// If the header is present it must be a valid UUIDv4, else 400.
// If missing, we generate one and inject it into the request + response headers.
func interactionIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-fapi-interaction-id")
		if id != "" {
			// Validate UUIDv4
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.Version() != 4 {
				http.Error(w, "invalid x-fapi-interaction-id", http.StatusBadRequest)
				return
			}
		} else {
			// Generate one if not provided
			newID := uuid.New()
			id = newID.String()

			// propagate the new ID downstream via request header
			r.Header.Set("x-fapi-interaction-id", id)
		}

		// ensure echo on response
		w.Header().Set("x-fapi-interaction-id", id)

		next.ServeHTTP(w, r)
	})
}

// requireScopes validates the caller's scopes.
// - 401 if Authorization header missing, token invalid, or inactive
// - 403 if token active but lacks the required scope(s)
func requireScopes(required ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes := scopesFromAuthorizer(r)

			// local/dev fallback to Authorization: Bearer <token>
			if len(scopes) == 0 {
				auth := r.Header.Get("Authorization")
				if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				token := strings.TrimSpace(auth[len("Bearer "):])
				parsedScopes, err := getScopes(token)
				if err != nil {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				scopes = parsedScopes
			}

			if !containsAllScopes(scopes, required) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"message": "scope not allowed",
				})
				return
			}

			// Store resolved scopes somewhere accessible to downstream handler.
			// Easiest is to just wrap r with a shallow clone, but since this is a demo
			// we'll re-derive again in sampleProtectedHandle via scopesFromRequest().
			next.ServeHTTP(w, r)
		})
	}
}

// scopesFromRequest consolidates the logic for returning all caller scopes
// for the current request. This is mainly for response/debug information.
// It mirrors the logic in requireScopes, but does not do any authz checks.
func scopesFromRequest(r *http.Request) []string {
	scopes := scopesFromAuthorizer(r)
	if len(scopes) > 0 {
		return scopes
	}

	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return nil
	}
	token := strings.TrimSpace(auth[len("Bearer "):])
	parsedScopes, err := getScopes(token)
	if err != nil {
		return nil
	}
	return parsedScopes
}

// callerInfoFromAuthorizer extracts identity-ish data from the API Gateway custom authorizer.
// In downstream forks, you would typically map these values to your own user model.
func callerInfoFromAuthorizer(r *http.Request) CallerInfo {
	if v1, ok := core.GetAPIGatewayContextFromContext(r.Context()); ok && v1.Authorizer != nil {
		var (
			sub        string
			givenName  string
			familyName string
		)

		if s, ok := v1.Authorizer["sub"].(string); ok && s != "" {
			sub = s
		}
		if gn, ok := v1.Authorizer["given_name"].(string); ok && gn != "" {
			givenName = gn
		}
		if fn, ok := v1.Authorizer["family_name"].(string); ok && fn != "" {
			familyName = fn
		}

		return CallerInfo{
			Subject:    sub,
			GivenName:  givenName,
			FamilyName: familyName,
		}
	}

	return CallerInfo{}
}

// scopesFromAuthorizer extracts the space-delimited "scope" claim from the custom authorizer context.
// Example: "sample.read sample.write"
func scopesFromAuthorizer(r *http.Request) []string {
	if v1, ok := core.GetAPIGatewayContextFromContext(r.Context()); ok && v1.Authorizer != nil {
		log.Println("authorizer:", v1.Authorizer)
		if s, ok := v1.Authorizer["scope"].(string); ok && s != "" {
			return strings.Fields(s)
		}
	}
	return nil
}

// getScopes parses a dev/local bearer token.
// The token can be:
// - a JSON string containing { "active": true, "scope": "s1 s2" } or arrays
// - a JWT-like JSON string carrying `scope`, `scopes`, or `permissions`
// This is intentionally permissive for local testing.
func getScopes(token string) ([]string, error) {
	var payload struct {
		Active      bool           `json:"active"`
		Scope       string         `json:"scope"`
		Scopes      []string       `json:"scopes"`
		Permissions []string       `json:"permissions"`
		Extra       map[string]any `json:"-"`
	}

	if err := json.Unmarshal([]byte(token), &payload); err != nil {
		return nil, err
	}

	switch {
	case len(payload.Scopes) > 0:
		return payload.Scopes, nil
	case len(payload.Permissions) > 0:
		return payload.Permissions, nil
	default:
		return splitScopes(payload.Scope), nil
	}
}

// splitScopes splits a space-delimited scope string like "sample.read sample.write".
func splitScopes(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func containsAllScopes(have []string, need []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	for _, req := range need {
		if _, ok := set[req]; !ok {
			return false
		}
	}
	return true
}

// RespondWithJson writes the given object as JSON.
func RespondWithJson(ctx context.Context, l *slog.Logger, w http.ResponseWriter, o any) {
	l.InfoContext(ctx, "responding with json", slog.Any("result", o))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(o); err != nil {
		l.ErrorContext(ctx, "error sending response", slog.Any("error", err))
		w.WriteHeader(http.StatusInternalServerError)
	}
}
