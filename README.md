# Sample API

A minimal HTTP API and custom authorizer designed to run as AWS Lambda functions behind API Gateway.  
It includes a mock API with scope-based access control, a token authorizer, and an optional mTLS proxy for testing secure client authentication.

---

## Overview

### Components
- **Authorizer**: validates OAuth2 tokens and supports mTLS-based client certificate checks.
- **Mock API**: exposes simple domain endpoints (`customer`, `energy`) with scope enforcement.
- **mTLS Proxy**: optional TLS termination layer for local or secure deployments.
- **LocalStack setup script**: provisions IAM roles, Lambdas, and API Gateway locally for testing.

---

## Mutual TLS (mTLS) Server

Located in `/cmd/mtls`, this service:
- Terminates TLS and enforces client certificate authentication.
- Validates certificates against a trusted CA bundle.
- Uses server and client keys stored in SSM (simulated via LocalStack).

---

## Features

- AWS Lambda–compatible handlers using `aws-lambda-go-api-proxy`.
- Example endpoints:
    - `GET /customer/v1/customer` → requires scope: `customer`
    - `GET /energy/v1/energy` → requires scope: `energy`
    - `GET /health` → liveness check
- Scope-based authorization via:
    - Lambda authorizer context (when deployed)
    - Bearer token fallback (for local use)
- `x-fapi-interaction-id` header validation (must be UUIDv4).

---

## Requirements

- Go 1.24+
- Docker
- LocalStack (for local AWS simulation)
- Optional AWS account for live testing

---

## Environment Variables

| Variable | Description |
|-----------|-------------|
| `AWS_LOCAL` | Set to `true` when running under LocalStack |
| `REGION` | AWS region (default `us-east-1`) |
| `CLIENT_CERT_HEADER` | Header name for mTLS client cert (e.g. `TLS-Certificate`) |
| `INTROSPECTION_ENDPOINT` | Token introspection URL *(leave blank locally)* |
| `USER_INFO_ENDPOINT` | User info URL *(leave blank locally)* |
| `CLIENT_ID` | OAuth2 client ID *(leave blank locally)* |
| `SSM_TRANSPORT_CERTIFICATE_NAME` | SSM name for server certificate |
| `SSM_TRANSPORT_KEY_NAME` | SSM name for server private key |
| `SSM_CA_TRUSTED_LIST_NAME` | SSM name for CA certificates |

When using LocalStack, TLS materials are automatically uploaded to SSM under `/sample-api/...`.

---

## Running locally
Option A: Go build/run
- This Lambda-oriented service is designed for API Gateway/Lambda. For local invocation you typically run with a Lambda runtime emulator (e.g., aws-lambda-rie) or SAM CLI.

Example with AWS SAM (simplified outline):
- Create a SAM template wiring API Gateway → Lambda (runtime: provided.al2, image-based or binary handler).
- Set env vars (AWS_LOCAL, REGION).
- Run: sam local start-api

Option B: Docker container
- The provided Dockerfile builds a minimal image suitable for local or image-based Lambda deployment.

Build:
- docker build -t mockapi:local .

Run against LocalStack:
- docker network create localstack || true
- docker run --rm -p 443:443
  --network localstack
  -e AWS_LOCAL=true
  -e REGION=eu-west-1
  --name mockapi
  mockapi:local

Notes:
- The container exposes port 443.
- Ensure LocalStack is reachable on the same Docker network as localstack.local:4566.

## Authorization and scopes
Each protected endpoint requires specific scopes:
- /sample/protected → sample

How scopes are resolved:
1. When behind API Gateway, the handler reads them from the custom authorizer context (scope as a space-delimited string).
2. Otherwise, it falls back to parsing the Authorization: Bearer token.

Accepted token formats for local/dev:
- JWT with a space-delimited scope claim in payload.
- A JSON string token that includes one of:
    - scope: "s1 s2"
    - scopes: ["s1","s2"]
    - permissions: ["s1","s2"]

Examples:
- JWT payload idea (pseudo): { "sub":"123", "scope":"sample" }
- JSON-string token example for sample: {"active":true,"scopes":["sample"]}

In practice, set an Authorization header like:
- Authorization: Bearer {"active":true,"scopes":["sample"]}

Responses on failure:
- 401 if Authorization is missing/invalid or introspection-style JSON cannot be parsed
- 403 if token is valid but lacks required scopes

## x-fapi-interaction-id
- If the client sets x-fapi-interaction-id, it must be a valid UUIDv4; otherwise the request is rejected with 400.
- If missing, the server generates a UUIDv4 and echoes it in the response header.

## Data persistence
- This sample is **stateless**. DynamoDB tables and seed data from the original demo were removed.

## Example requests (local)
Protected endpoint (requires `sample` scope):
- curl -i https://localhost:443/sample/protected \
  -H 'x-fapi-interaction-id: 3fa85f64-5717-4562-b3fc-2c963f66afa6' \
  -H 'Authorization: Bearer {"active":true,"scopes":["sample"]}'

Missing or invalid x-fapi-interaction-id:
- If you pass x-fapi-interaction-id with an invalid format, you will get 400.
- If you omit it, the response will include a generated x-fapi-interaction-id.

## Deployment
Container image (typical for Lambda):
- Build and push the image to ECR.
- Create/update a Lambda function using the container image.
- Configure an API Gateway HTTP API or REST API to route to the Lambda.
- Configure a custom authorizer (if applicable) to provide the scope field in the authorizer context.
- Set env vars (REGION, POPULATE_DB as needed; do not set AWS_LOCAL in production).

IAM and permissions:
- The Lambda role must allow access to DynamoDB (read/write as needed for your tables).

## Troubleshooting
- 401 Unauthorized: Missing Authorization header, malformed token, or token content cannot be parsed for scopes.
- 403 Forbidden: Token valid but does not include required scope.
- 400 Bad Request: x-fapi-interaction-id provided but not a valid UUIDv4.
- 404 Not Found: No matching data in DynamoDB (ensure POPULATE_DB or seed data).
- DynamoDB local connection issues: Verify Docker network and that LocalStack is reachable at [http://localstack.local:4566](http://localstack.local:4566) with AWS_LOCAL=true.
