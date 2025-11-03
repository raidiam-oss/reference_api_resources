#!/bin/bash

unset AWS_PROFILE AWS_VAULT
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_SESSION_TOKEN=test
export AWS_REGION=us-east-1

# Exit immediately if any command fails.
set -e

########################################
# Store mTLS material in SSM Parameter Store
# Renamed prefix to /sample-api/...
########################################

awslocal ssm put-parameter \
  --name "/sample-api/ca-crt" \
  --type "SecureString" \
  --value "$(cat /keys/ca.crt)" \
  --overwrite

awslocal ssm put-parameter \
  --name "/sample-api/server-key" \
  --type "SecureString" \
  --value "$(cat /keys/server.key)" \
  --overwrite

awslocal ssm put-parameter \
  --name "/sample-api/server-crt" \
  --type "SecureString" \
  --value "$(cat /keys/server.crt)" \
  --overwrite

########################################
# Ensure IAM role for Lambda exists (authorizer + mock)
########################################

ROLE_NAME="authorizer-lambda-role"

echo "Ensuring IAM role '$ROLE_NAME' exists..."
if ! ROLE_ARN="$(awslocal iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null)"; then
  TRUST_POLICY='{
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Principal": { "Service": "lambda.amazonaws.com" },
        "Action": "sts:AssumeRole"
      }
    ]
  }'

  ROLE_ARN="$(awslocal iam create-role \
    --role-name "$ROLE_NAME" \
    --assume-role-policy-document "$TRUST_POLICY" \
    --query 'Role.Arn' \
    --output text)"

  # Attach basic execution policy recognized by LocalStack
  awslocal iam attach-role-policy \
    --role-name "$ROLE_NAME" \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole >/dev/null
  echo "Created IAM role: $ROLE_ARN"
else
  echo "Using existing IAM role: $ROLE_ARN"
fi

########################################
# Create Lambda functions (authorizer + mock API)
########################################

# NOTE:
# We intentionally leave these **blank** for the OSS repo.
# Users must provide real values (or local stubs) before invoking the authorizer.
INTROSPECTION_ENDPOINT="${INTROSPECTION_ENDPOINT:-}"
USER_INFO_ENDPOINT="${USER_INFO_ENDPOINT:-}"
CLIENT_ID="${CLIENT_ID:-}"
CLIENT_CERT_HEADER="${CLIENT_CERT_HEADER:-TLS-Certificate}"

if [ -z "$INTROSPECTION_ENDPOINT" ] || [ -z "$USER_INFO_ENDPOINT" ] || [ -z "$CLIENT_ID" ]; then
  echo "WARNING: INTROSPECTION_ENDPOINT / USER_INFO_ENDPOINT / CLIENT_ID are not set."
  echo "The authorizer Lambda will fail at invocation time until you set them."
fi

SSM_TRANSPORT_CERTIFICATE_NAME="/sample-api/server-crt"
SSM_TRANSPORT_KEY_NAME="/sample-api/server-key"
SSM_CA_TRUSTED_LIST_NAME="/sample-api/ca-crt"

# Environment for the custom authorizer Lambda
AUTH_ENV_VARS="Variables={LOG_LEVEL=TRACE,AWS_REGION=${AWS_REGION},CLIENT_CERT_HEADER=${CLIENT_CERT_HEADER},AWS_LOCAL=true,LOCALSTACK_ENDPOINT=http://localhost:4566,AWS_ACCESS_KEY_ID=fake,AWS_SECRET_ACCESS_KEY=fake,KMS_KEY_ID=alias/authorizer-key,INTROSPECTION_ENDPOINT=${INTROSPECTION_ENDPOINT},CLIENT_ID=${CLIENT_ID},USER_INFO_ENDPOINT=${USER_INFO_ENDPOINT},SSM_TRANSPORT_CERTIFICATE_NAME=${SSM_TRANSPORT_CERTIFICATE_NAME},SSM_TRANSPORT_KEY_NAME=${SSM_TRANSPORT_KEY_NAME},SSM_CA_TRUSTED_LIST_NAME=${SSM_CA_TRUSTED_LIST_NAME}}"

echo "Creating Lambda function 'authorizer'..."
awslocal lambda create-function \
    --function-name "authorizer" \
    --runtime "provided.al2" \
    --handler "bootstrap" \
    --role "$ROLE_ARN" \
    --zip-file "fileb:///builds/authorizer/sample-api-1.0.0.authorizer-function.zip" \
    --environment "$AUTH_ENV_VARS" \
    --timeout 30 \
    --region "$AWS_REGION" >/dev/null || echo "authorizer already exists (continuing)"

echo "Lambda 'authorizer' ensured."

# Environment for the mock API Lambda (stateless demo)
MOCK_ENV_VARS="Variables={LOG_LEVEL=TRACE}"

echo "Creating Lambda function 'mock'..."
awslocal lambda create-function \
    --function-name "mock" \
    --runtime "provided.al2" \
    --handler "bootstrap" \
    --role "$ROLE_ARN" \
    --zip-file 'fileb:///builds/mock/sample-api-1.0.0.mock-api-function.zip' \
    --environment "$MOCK_ENV_VARS" \
    --timeout 30 \
    --region "$AWS_REGION" >/dev/null || echo "mock already exists (continuing)"

echo "Lambda 'mock' ensured."

########################################
# Publish OpenAPI to API Gateway and wire Lambdas
########################################

echo "Fetching Lambda ARN for function 'authorizer'..."
AUTH_LAMBDA_ARN="$(awslocal lambda get-function --function-name "authorizer" --region "$AWS_REGION" --query 'Configuration.FunctionArn' --output text)"

echo "Fetching Lambda ARN for function 'mock'..."
API_LAMBDA_ARN="$(awslocal lambda get-function --function-name "mock" --region "$AWS_REGION" --query 'Configuration.FunctionArn' --output text)"

# Inject Lambda ARNs into the OpenAPI template and produce apib.yaml
sed -e "s|\${demo_service_invoke_arn}|arn:aws:apigateway:${AWS_REGION}:lambda:path/2015-03-31/functions/${API_LAMBDA_ARN}:\$LATEST/invocations|g" \
    -e "s|\${authorizer_invoke_arn}|arn:aws:apigateway:${AWS_REGION}:lambda:path/2015-03-31/functions/${AUTH_LAMBDA_ARN}/invocations|g" \
    /builds/openapi/api.yml > /builds/openapi/apib.yaml

echo "Importing OpenAPI into API Gateway v2 (REST API emulation under LocalStack)..."
REST_API_ID="$(awslocal apigateway import-rest-api \
    --body "file:///builds/openapi/apib.yaml" \
    --region "$AWS_REGION" \
    --query 'id' --output text)"

echo "Created/updated API Gateway REST API: $REST_API_ID"

# Create deployment + stage "v1"
DEPLOYMENT_ID="$(awslocal apigateway create-deployment \
    --rest-api-id "$REST_API_ID" \
    --stage-name "v1" \
    --region "$AWS_REGION" \
    --query 'id' --output text)"

echo "Deployment: $DEPLOYMENT_ID"

# HTTP API base URL in LocalStack
INVOKE_URL="http://localstack:4566/restapis/${REST_API_ID}/v1/_user_request_/"
echo "Invoke URL: $INVOKE_URL"

########################################
# Allow API Gateway to invoke authorizer Lambda
########################################

awslocal lambda add-permission \
  --function-name authorizer \
  --statement-id apigw-authorizer \
  --action lambda:InvokeFunction \
  --principal apigateway.amazonaws.com \
  --source-arn arn:aws:execute-api:${AWS_REGION}:000000000000:${REST_API_ID}/* \
  >/dev/null 2>&1 || echo "Invoke permission for authorizer already exists"

echo "LocalStack setup complete."