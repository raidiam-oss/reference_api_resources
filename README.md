# Sample API (reference_api_resources)

A minimal **reference implementation** of an HTTP API, custom token authorizer, and optional mTLS proxy designed to run as AWS Lambda functions behind API Gateway.

The repository is intended as a **forkable, production-inspired template** for teams experimenting with:

- Secure API patterns (OAuth2 scopes, custom authorizers, mTLS, API Gateway flows)
- Local AWS-like environments using LocalStack
- Infrastructure validation, integration testing, and client onboarding flows

This project mirrors the design standards used in the Recco repositories, but is generalized for any domain.

---

# Table of Contents
1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Folder Structure](#folder-structure)
4. [Requirements](#requirements)
5. [Getting Started (Local)](#getting-started-local)
6. [Environment Variables](#environment-variables)
7. [Running the Stack](#running-the-stack)
8. [Testing the API](#testing-the-api)
9. [Example Flows](#example-flows)
10. [Deployment](#deployment)
11. [Troubleshooting](#troubleshooting)

---

# Overview

This repository contains:

- **Mock API Lambda** – A simple, scope-protected endpoint (`/sample/protected`).
- **Custom Authorizer Lambda** – Validates tokens and resolves scopes.
- **mTLS Proxy** – Terminates TLS, validates client certificates, and forwards traffic to API Gateway.
- **LocalStack automation** – Fully provisions IAM roles, Lambdas, API Gateway, and SSM.
- **Local development CA + certificates** – Used exclusively for local testing.

The goal is to be:

- Simple
- Understandable
- Easily forkable
- Close to real AWS behaviour

---

# Architecture

## High-level Flow

```
Client
  │ (HTTPS + optional mTLS)
  ▼
mTLS Proxy (port 443)
  │ forwards → execute-api
  ▼
API Gateway (LocalStack or AWS)
 ├── Custom Authorizer Lambda
 └── Mock API Lambda
```

### Components

| Component | Purpose |
|----------|---------|
| **Mock API** | Implements `/sample/protected`; enforces the `sample` scope. |
| **Authorizer** | Reads OAuth2-style tokens, extracts scopes, returns IAM-style policy results. |
| **mTLS proxy** | Loads certs from SSM, terminates TLS, enforces client certs, forwards to the API. |
| **LocalStack** | Provides Lambda, SSM, IAM, and API Gateway locally. |
| **Key materials** | Self-signed CA, server keypair, and client cert for testing. |

---

# Folder Structure

```
reference_api_resources/
│
├── cmd/
│   └── mtls/                 # mTLS proxy (TLS termination, SSM cert loading)
│
├── authorizer/               # Custom authorizer Lambda
│
├── mockapi/                  # Sample mock API Lambda
│
├── infra/                    # LocalStack provisioning scripts (API Gateway, IAM, Lambdas)
│
├── keys/                     # Local CA, server certs, client certs
│
├── docker-compose.yml        # Entire local runtime stack
└── README.md                 # This documentation
```

---

# Requirements

## Core Tools
- Go **1.24+**
- Docker **20+**
- Docker Compose
- Make (optional)

## Local AWS Simulation
- LocalStack (managed through docker-compose)
- AWS CLI v2
    - Credentials may be dummy (`test/test`)

## TLS Tooling
- OpenSSL (or mkcert)

## Optional for AWS deployment
- AWS Account
- ECR, Lambda, API Gateway, SSM permissions

---

# Getting Started (Local)

The project includes a fully automated LocalStack environment. The only required command is:

```
docker compose up --build
```

This launches:

- mTLS proxy on **https://localhost**
- Authorizer Lambda
- Mock API Lambda
- API Gateway REST API
- SSM with uploaded certificates

---

## 1. Generate Local TLS Certificates

Inside `keys/`:

```
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days 365 \
  -subj "/CN=Local CA" \
  -out ca.crt

openssl genrsa -out server.key 2048
openssl req -new -key server.key -subj "/CN=mtls-api.local" -out server.csr

openssl x509 -req -in server.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -days 365 -sha256

openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=mtls-client" -out client.csr

openssl x509 -req -in client.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out client.crt -days 365 -sha256
```

These are automatically uploaded to SSM by LocalStack at startup.

---

# Environment Variables

| Variable | Description |
|----------|-------------|
| `AWS_LOCAL` | Whether to use LocalStack (`true`) |
| `REGION` | AWS region (default: `us-east-1`) |
| `CLIENT_CERT_HEADER` | Header containing forwarded client cert |
| `INTROSPECTION_ENDPOINT` | Token introspection URL *(unused locally)* |
| `USER_INFO_ENDPOINT` | User-info endpoint *(unused locally)* |
| `CLIENT_ID` | OAuth2 client ID *(unused locally)* |
| `SSM_TRANSPORT_CERTIFICATE_NAME` | Path to server cert in SSM |
| `SSM_TRANSPORT_KEY_NAME` | Path to server key |
| `SSM_CA_TRUSTED_LIST_NAME` | Path to CA bundle |

Defaults point to:

```
/sample-api/server-crt
/sample-api/server-key
/sample-api/ca-crt
```

---

# Running the Stack

```
docker compose up --build
```

This will:

- Deploy Lambdas
- Deploy API Gateway
- Load certificates to SSM
- Start the mTLS proxy on port **443**

---

# Testing the API

## 1. Test health endpoint (HTTPS + client cert)

Using Postman:

- CA cert: `keys/ca.crt`
- Client cert: `keys/client.crt`
- Client key: `keys/client.key`

Send:

```
GET https://localhost/health
```

Expected response:

```
{"status":"ok"}
```

---

## 2. Test protected endpoint (scope required)

```
GET https://localhost/sample/protected
Authorization: Bearer {"active":true,"scopes":["sample"]}
x-fapi-interaction-id: <valid UUIDv4>
```

Expected:

```
200 OK
```

If missing the scope `"sample"`:

```
403 Forbidden
```

If missing Authorization header:

```
401 Unauthorized
```

If invalid UUID:

```
400 Bad Request
```

---

# Example Flows

## Token + Authorizer flow
1. Client sends Authorization header
2. API Gateway invokes custom authorizer
3. Authorizer returns IAM-style policy
4. API Lambda executes with scope context

## mTLS flow
1. Client presents certificate
2. Proxy validates against CA from SSM
3. Proxy forwards request to execute-api
4. API Gateway + Lambdas run as normal

---

# Troubleshooting

| Problem | Likely Cause |
|--------|--------------|
| 308 redirect | Wrong execute-api path (double slash); check proxy logs |
| 401 | Missing/invalid Authorization header |
| 403 | Token valid but missing required scope |
| 400 | Invalid UUID in `x-fapi-interaction-id` |
| mTLS handshake failure | Wrong CA, wrong key, unsupported key format on macOS |
| LocalStack not provisioning | Run `docker compose logs localstack` |

---