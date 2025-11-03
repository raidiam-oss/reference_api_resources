package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-xray-sdk-go/xray"
)

var (
	l                     *slog.Logger
	clientID              string
	introspectionEndpoint string
	userInfoEndpoint      string
	clientCertHeader      string
	httpClient            = http.DefaultClient
	errUnauthorized       = errors.New("Unauthorized")
	cert, key, ca         []byte

	denyAllAuthResponse = events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: "user",
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version: "2012-10-17",
			Statement: []events.IAMPolicyStatement{
				{
					Action:   []string{"execute-api:Invoke"},
					Effect:   "Deny",
					Resource: []string{"*"},
				},
			},
		},
	}
)

// --- lightweight context helpers (API Gateway authorizer allows only string/number/bool) ---

type AuthorizerResponseContext struct {
	Sub         *string                `json:"sub,omitempty"`
	GivenName   *string                `json:"given_name,omitempty"`
	FamilyName  *string                `json:"family_name,omitempty"`
	Birthdate   *string                `json:"birthdate,omitempty"`
	Address     *string                `json:"address,omitempty"`
	ClientID    *string                `json:"client_id,omitempty"`
	Scope       *string                `json:"scope,omitempty"`
	X5tsha256   *string                `json:"x5t#S256,omitempty"`
	AccessToken *string                `json:"access_token,omitempty"` // usually too big; include only if you really need it
	UserInfo    map[string]interface{} `json:"user_info,omitempty"`
}

func (c AuthorizerResponseContext) ToMap() map[string]interface{} {
	m := map[string]interface{}{}
	if c.Sub != nil {
		m["sub"] = *c.Sub
	}
	if c.GivenName != nil {
		m["given_name"] = *c.GivenName
	}
	if c.FamilyName != nil {
		m["family_name"] = *c.FamilyName
	}
	if c.Birthdate != nil {
		m["birthdate"] = *c.Birthdate
	}
	if c.Address != nil {
		m["address"] = *c.Address
	}
	if c.ClientID != nil {
		m["client_id"] = *c.ClientID
	}
	if c.Scope != nil {
		m["scope"] = *c.Scope
	}
	if c.X5tsha256 != nil {
		m["x5t#S256"] = *c.X5tsha256
	}
	// Caution: API Gateway limits context size; large maps may be truncated.
	if len(c.UserInfo) > 0 {
		m["user_info"] = c.UserInfo
	}
	// Avoid putting AccessToken in context in production; left here for parity.
	// if c.AccessToken != nil { m["access_token"] = *c.AccessToken }
	return m
}

func ptr(s string) *string { return &s }

// --- token / cnf models ---

type cnfResponse struct {
	X5TSha256 string `json:"x5t#S256"`
}

type introspectionResponse struct {
	Scope      string      `json:"scope"`
	Active     bool        `json:"active"`
	TokenType  string      `json:"token_type"`
	Exp        int         `json:"exp"`
	ClientID   string      `json:"client_id"`
	Subject    string      `json:"sub"`
	GivenName  string      `json:"given_name"`
	FamilyName string      `json:"family_name"`
	BirthDate  string      `json:"birthdate"`
	Address    string      `json:"address"`
	CNF        cnfResponse `json:"cnf,omitempty"`
}

// --- helpers ---

func normalisePEM(originalPEM string) string {
	const beginCert = "-----BEGIN CERTIFICATE-----"
	const endCert = "-----END CERTIFICATE-----"
	clean := strings.ReplaceAll(originalPEM, beginCert, "")
	clean = strings.ReplaceAll(clean, endCert, "")
	clean = strings.ReplaceAll(clean, " ", "\n")
	return fmt.Sprintf("%s\n%s\n%s", beginCert, clean, endCert)
}

func calculateX5TSha256(ctx context.Context, certData string) (string, error) {
	derBytes, _ := pem.Decode([]byte(certData))
	if derBytes == nil {
		return "", errors.New("failed to decode certificate")
	}
	c, err := x509.ParseCertificate(derBytes.Bytes)
	if err != nil {
		l.ErrorContext(ctx, "error parsing certificate", slog.String("error", err.Error()))
		return "", err
	}
	fingerprint := sha256.Sum256(c.Raw)
	return base64.RawURLEncoding.EncodeToString(fingerprint[:]), nil
}

func getTokenFromHeader(headers map[string]string) (string, error) {
	auth := headers["Authorization"]
	if auth == "" {
		auth = headers["authorization"]
	}
	rx := regexp.MustCompile(`^[bB]earer (.*)$`)
	m := rx.FindStringSubmatch(auth)
	if len(m) != 2 {
		return "", fmt.Errorf("unable to extract bearer token from header")
	}
	return m[1], nil
}

func generatePolicy(principalID, resource string, ctxMap map[string]interface{}) events.APIGatewayCustomAuthorizerResponse {
	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: principalID,
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version: "2012-10-17",
			Statement: []events.IAMPolicyStatement{
				{
					Action:   []string{"execute-api:Invoke"},
					Effect:   "Allow",
					Resource: []string{resource},
				},
			},
		},
		Context: ctxMap,
	}
}

// --- SSM / HTTP calls ---

func loadTlsFromSSM(ctx context.Context, certName, keyName, caName string) (certPEM, keyPEM, caPEM []byte, err error) {
	local := strings.ToLower(os.Getenv("AWS_LOCAL")) == "true"
	region := os.Getenv("REGION")

	awsCfg, _ := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
	)

	var client *ssm.Client
	if local {
		client = ssm.NewFromConfig(awsCfg, func(o *ssm.Options) {
			o.BaseEndpoint = aws.String("http://localstack.local:4566")
			o.Credentials = credentials.NewStaticCredentialsProvider("test", "test", "")
		})
	} else {
		client = ssm.NewFromConfig(awsCfg)
	}

	get := func(name string, decrypt bool) ([]byte, error) {
		slog.Info("loading cert material from SSM", slog.String("name", name))
		out, e := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(decrypt),
		})
		if e != nil {
			return nil, e
		}
		if out.Parameter == nil || out.Parameter.Value == nil {
			return nil, fmt.Errorf("parameter %s is empty", name)
		}
		return []byte(*out.Parameter.Value), nil
	}

	certPEM, err = get(certName, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get cert: %w", err)
	}
	keyPEM, err = get(keyName, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get key: %w", err)
	}
	caPEM, err = get(caName, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get ca: %w", err)
	}

	return certPEM, keyPEM, caPEM, nil
}

func introspectToken(ctx context.Context, token string, aCtx *AuthorizerResponseContext) (*introspectionResponse, error) {
	l.InfoContext(ctx, "introspecting token")

	form := url.Values{}
	form.Add("token", token)
	form.Add("client_id", os.Getenv("CLIENT_ID"))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, introspectionEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		slog.ErrorContext(ctx, "failed to build introspection request", slog.String("error", err.Error()))
		return nil, errUnauthorized
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	clientCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		log.Fatalf("failed to load client cert/key pair: %v", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if rootCAs == nil || err != nil {
		rootCAs = x509.NewCertPool()
	}
	if ok := rootCAs.AppendCertsFromPEM(ca); !ok {
		log.Fatalf("failed to append CA bundle")
	}

	mtlsClient := xray.Client(&http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      rootCAs,
			MinVersion:   tls.VersionTLS12,
		}},
	})

	resp, err := mtlsClient.Do(req)
	if err != nil {
		slog.ErrorContext(ctx, "failed to call introspection", slog.String("error", err.Error()))
		return nil, errUnauthorized
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.ErrorContext(ctx, "unable to read introspection response", slog.String("error", err.Error()))
		return nil, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		slog.ErrorContext(ctx, "unexpected status on introspection", slog.Int("status", resp.StatusCode))
		return nil, errUnauthorized
	}

	var iResp introspectionResponse
	if err := json.Unmarshal(body, &iResp); err != nil {
		slog.ErrorContext(ctx, "error parsing introspection json", slog.String("error", err.Error()))
		return nil, errUnauthorized
	}

	aCtx.AccessToken = ptr(string(body))
	aCtx.ClientID = &iResp.ClientID
	aCtx.Scope = &iResp.Scope
	aCtx.X5tsha256 = &iResp.CNF.X5TSha256

	if !iResp.Active {
		slog.InfoContext(ctx, "token not active", slog.Any("introspectionResponse", iResp))
		return nil, errUnauthorized
	}
	return &iResp, nil
}

func getUserInfo(ctx context.Context, token string, aCtx *AuthorizerResponseContext) error {
	l.InfoContext(ctx, "retrieving user info response")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoEndpoint, nil)
	if err != nil {
		return errUnauthorized
	}
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	req.Header.Add("authorization", fmt.Sprintf("Bearer %s", token))

	clientCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		log.Fatalf("failed to load client cert/key pair: %v", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if rootCAs == nil || err != nil {
		rootCAs = x509.NewCertPool()
	}
	if ok := rootCAs.AppendCertsFromPEM(ca); !ok {
		log.Fatalf("failed to append CA bundle")
	}

	mtlsClient := xray.Client(&http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      rootCAs,
			MinVersion:   tls.VersionTLS12,
		}},
	})

	resp, err := mtlsClient.Do(req)
	if err != nil {
		l.ErrorContext(ctx, "unable to call user info endpoint", slog.String("error", err.Error()))
		return errUnauthorized
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		l.ErrorContext(ctx, "unexpected status on user info response", slog.Int("status", resp.StatusCode))
		return errUnauthorized
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.ErrorContext(ctx, "unable to read user info response body", slog.String("error", err.Error()))
		return errUnauthorized
	}

	var ui map[string]interface{}
	if err := json.Unmarshal(body, &ui); err != nil {
		l.ErrorContext(ctx, "error unmarshalling user info", slog.String("error", err.Error()))
		return errUnauthorized
	}
	aCtx.UserInfo = ui
	return nil
}

func validateCnf(ctx context.Context, iResp introspectionResponse, clientCert string) error {
	if clientCert == "" {
		l.ErrorContext(ctx, fmt.Sprintf("no %s found on request", clientCertHeader))
		return errUnauthorized
	}

	b64, err := calculateX5TSha256(ctx, normalisePEM(clientCert))
	if err != nil {
		l.ErrorContext(ctx, "error calculating base64 thumbprint", slog.String("error", err.Error()))
		return errUnauthorized
	}
	if b64 != iResp.CNF.X5TSha256 {
		l.ErrorContext(ctx, "certificate thumbprint does not match token cnf.x5t#S256")
		return errUnauthorized
	}
	return nil
}

// --- lambda entrypoint ---

func handleRequest(ctx context.Context, event events.APIGatewayProxyRequest) (events.APIGatewayCustomAuthorizerResponse, error) {
	l.InfoContext(ctx, "authenticating request")

	// TLS materials from SSM (LocalStack or real AWS)
	ssmCertName := os.Getenv("SSM_TRANSPORT_CERTIFICATE_NAME")
	ssmKeyName := os.Getenv("SSM_TRANSPORT_KEY_NAME")
	ssmCAName := os.Getenv("SSM_CA_TRUSTED_LIST_NAME")

	var err error
	cert, key, ca, err = loadTlsFromSSM(context.Background(), ssmCertName, ssmKeyName, ssmCAName)
	if err != nil {
		log.Fatalf("failed to load TLS materials from SSM: %v", err)
	}

	token, err := getTokenFromHeader(event.Headers)
	if err != nil {
		l.ErrorContext(ctx, "malformed authorization header", slog.String("error", err.Error()))
		return denyAllAuthResponse, errUnauthorized
	}

	aCtx := AuthorizerResponseContext{}
	iResp, err := introspectToken(ctx, token, &aCtx)
	if err != nil {
		l.ErrorContext(ctx, "unable to introspect token, rejecting", slog.String("error", err.Error()))
		return denyAllAuthResponse, errUnauthorized
	}

	// Copy select claims to authorizer context
	if iResp.Subject != "" {
		aCtx.Sub = ptr(iResp.Subject)
	}
	if iResp.FamilyName != "" {
		aCtx.FamilyName = ptr(iResp.FamilyName)
	}
	if iResp.GivenName != "" {
		aCtx.GivenName = ptr(iResp.GivenName)
	}
	if iResp.BirthDate != "" {
		aCtx.Birthdate = ptr(iResp.BirthDate)
	}
	if iResp.Address != "" {
		aCtx.Address = ptr(iResp.Address)
	}

	// Optional: fetch userinfo (comment out if you want to allow without it)
	if err := getUserInfo(ctx, token, &aCtx); err != nil {
		l.ErrorContext(ctx, "user info retrieval failed", slog.String("error", err.Error()))
		return denyAllAuthResponse, errUnauthorized
	}

	// Authorize everything for this API (resource wildcard). Adjust as needed.
	lc := event.RequestContext
	resourceARN := fmt.Sprintf("arn:aws:execute-api:*:%s:%s/%s/%s/%s", lc.AccountID, lc.APIID, lc.Stage, "*", "*")

	principal := clientID
	if iResp.Subject != "" {
		principal = fmt.Sprintf("%s-%s", clientID, iResp.Subject)
	}

	// Enforce cnf if present
	if iResp.CNF.X5TSha256 != "" {
		clientCert := event.Headers[clientCertHeader]
		if err := validateCnf(ctx, *iResp, clientCert); err != nil {
			return denyAllAuthResponse, err
		}
	}

	return generatePolicy(principal, resourceARN, aCtx.ToMap()), nil
}

func main() {
	_ = os.Setenv("AWS_XRAY_SDK_DISABLED", "TRUE")
	l = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	httpClient = xray.Client(http.DefaultClient)

	for _, v := range []string{
		"INTROSPECTION_ENDPOINT",
		"USER_INFO_ENDPOINT",
		"CLIENT_ID",
	} {
		if _, found := os.LookupEnv(v); !found {
			msg := fmt.Sprintf("environment variable %s not set", v)
			l.ErrorContext(ctx, msg)
			panic(msg)
		}
	}

	clientID = os.Getenv("CLIENT_ID")
	introspectionEndpoint = os.Getenv("INTROSPECTION_ENDPOINT")
	userInfoEndpoint = os.Getenv("USER_INFO_ENDPOINT")
	clientCertHeader = os.Getenv("CLIENT_CERT_HEADER")

	_ = os.Unsetenv("AWS_XRAY_SDK_DISABLED")
	l.InfoContext(ctx, "authorizer lambda started")
	lambda.Start(handleRequest)
}
