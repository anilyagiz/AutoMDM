#!/bin/bash
set -euo pipefail

STACK_NAME="${STACK_NAME:-automdm-elasticache-iam-test}"

echo "🧹 Cleaning up test environment..."

# Preflight
if ! command -v aws >/dev/null 2>&1; then
    echo "❌ aws CLI not found in PATH. Please install AWS CLI and try again."
    exit 1
fi
if [ -f test-env-info.txt ]; then
    source test-env-info.txt
    
    echo "📦 Deleting EC2 key pair..."
    aws ec2 delete-key-pair \
        --key-name "${SSH_KEY%.pem}" || true
    
    rm -f "$SSH_KEY"
fi

echo "🗑️  Deleting CloudFormation stack..."
aws cloudformation delete-stack \
    --stack-name "$STACK_NAME"

echo "⏳ Waiting for stack deletion to complete..."
aws cloudformation wait stack-delete-complete \
    --stack-name "$STACK_NAME"

rm -f test-env-info.txt

echo "✅ Cleanup complete!"