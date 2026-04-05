#!/bin/bash
set -e

MODEL_DIR="${MODEL_DIR:-./models}"
MODEL_NAME="${MODEL_NAME:-nomic-embed-text-v1.5.Q4_K_M.gguf}"
MODEL_URL="${MODEL_URL:-https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/$MODEL_NAME}"

mkdir -p "$MODEL_DIR"

if [ -f "$MODEL_DIR/$MODEL_NAME" ]; then
    echo "Model already exists: $MODEL_DIR/$MODEL_NAME"
    exit 0
fi

echo "Downloading $MODEL_NAME to $MODEL_DIR..."
curl -L -o "$MODEL_DIR/$MODEL_NAME" "$MODEL_URL"

echo "Download complete!"
ls -lh "$MODEL_DIR/$MODEL_NAME"
