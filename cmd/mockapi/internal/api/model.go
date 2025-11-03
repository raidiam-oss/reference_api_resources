package api

// CallerInfo represents identity-style data extracted from the request's
// API Gateway custom authorizer context.
// Downstream users are expected to extend or replace this with their own fields.
//
// Example of what might be populated by the platform authorizer:
//
//	{
//	  "sub": "user-12345",
//	  "given_name": "Alice",
//	  "family_name": "Doe",
//	  "scope": "sample.read sample.write"
//	}
//
// Only a few common fields are exposed here to avoid leaking any
// domain-specific concepts.
type CallerInfo struct {
	Subject    string `json:"sub,omitempty"`
	GivenName  string `json:"given_name,omitempty"`
	FamilyName string `json:"family_name,omitempty"`
}

// SampleResponse is the JSON body returned by the demo protected endpoint
// (/sample/protected).
//
// It demonstrates:
//   - how scopes were resolved
//   - what identity info was available
//   - what x-fapi-interaction-id was enforced/echoed
//
// This struct is intentionally generic and safe for reuse.
type SampleResponse struct {
	Message           string     `json:"message"`
	Scopes            []string   `json:"scopes"`
	Caller            CallerInfo `json:"caller"`
	FapiInteractionID string     `json:"x_fapi_interaction_id"`
}
