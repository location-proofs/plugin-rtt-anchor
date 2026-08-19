#!/usr/bin/env bash
set -e

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

echo "[1/3] Cleaning old shared objects..."
rm -f "${DIR}/libgpuprover.so" "${DIR}/libgpuprover.a" "${DIR}/gpu_prover.o" "${DIR}/attester"

echo "[2/3] Compiling CUDA static archive (libgpuprover.a)..."
nvcc -O3 -c -Xcompiler -fPIC -o "${DIR}/gpu_prover.o" "${DIR}/gpu_prover.cu"
ar rcs "${DIR}/libgpuprover.a" "${DIR}/gpu_prover.o"

echo "[3/3] Compiling Go attester binary..."
CGO_ENABLED=1 go build -o "${DIR}/attester" "${DIR}/main.go"

echo "Build complete: ${DIR}/attester"