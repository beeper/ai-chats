#!/bin/bash
# Generate AI models Go file from OpenRouter API
# Usage: ./generate-models.sh [--openrouter-token="YOUR_TOKEN"]
#
# This script fetches model capabilities from OpenRouter and generates
# a Go file with model definitions. The generated file is checked into
# version control to provide a stable, known model list.

set -e

# Parse arguments
OPENROUTER_TOKEN=""
OUTPUT_FILE="bridges/ai/beeper_models_generated.go"
JSON_FILE="pkg/ai/beeper_models.json"

while [[ $# -gt 0 ]]; do
  case $1 in
    --openrouter-token=*)
      OPENROUTER_TOKEN="${1#*=}"
      shift
      ;;
    --output=*)
      OUTPUT_FILE="${1#*=}"
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [--openrouter-token=TOKEN] [--output=FILE]"
      echo ""
      echo "Options:"
      echo "  --openrouter-token=TOKEN  Optional OpenRouter API token"
      echo "  --output=FILE             Output file path (default: bridges/ai/beeper_models_generated.go)"
      echo "  --json=FILE               Output JSON path (default: pkg/ai/beeper_models.json)"
      exit 0
      ;;
    --json=*)
      JSON_FILE="${1#*=}"
      shift
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Change to script directory
cd "$(dirname "$0")"

# Run the generator
echo "Generating models from OpenRouter API..."
go run ./cmd/generate-models/main.go --openrouter-token="$OPENROUTER_TOKEN" --output="$OUTPUT_FILE" --json="$JSON_FILE"

echo "Generated: $OUTPUT_FILE"
echo "Generated: $JSON_FILE"
echo "Don't forget to check in the generated file!"
