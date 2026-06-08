#!/usr/bin/env bash
# Build llama.cpp as a static library for agentvault's embedded inference engine.
# Outputs: third_party/llama/lib/libllama.a  third_party/llama/include/llama.h
#
# Usage:
#   bash scripts/build-llama.sh              # latest master
#   LLAMA_TAG=b4760 bash scripts/build-llama.sh   # pin to a build tag
#
# Re-running is a no-op if the library already exists (remove third_party/llama/ to rebuild).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
OUT_DIR="${PROJECT_DIR}/third_party/llama"
LLAMA_REPO="${LLAMA_REPO:-https://github.com/ggerganov/llama.cpp}"
LLAMA_TAG="${LLAMA_TAG:-}"   # empty = clone latest master

if [ -z "${LLAMA_TAG}" ]; then
    echo "WARNING: LLAMA_TAG is unset — building against llama.cpp master (not reproducible)."
    echo "         Set LLAMA_TAG=bNNNN to pin to a specific release tag."
fi

if [ -f "${OUT_DIR}/lib/libllama.a" ]; then
    echo "llama.cpp static library already built at ${OUT_DIR}/lib/libllama.a"
    echo "Remove third_party/llama/ to force a rebuild."
    exit 0
fi

echo "==> Cloning llama.cpp..."
mkdir -p "${OUT_DIR}"
if [ ! -d "${OUT_DIR}/src/.git" ]; then
    if [ -n "${LLAMA_TAG}" ]; then
        git clone --depth 1 --branch "${LLAMA_TAG}" "${LLAMA_REPO}" "${OUT_DIR}/src"
    else
        git clone --depth 1 "${LLAMA_REPO}" "${OUT_DIR}/src"
    fi
else
    echo "    Source already cloned — skipping fetch"
fi

echo "==> Configuring cmake..."
cmake -S "${OUT_DIR}/src" -B "${OUT_DIR}/build" \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF \
    -DLLAMA_BUILD_TESTS=OFF \
    -DLLAMA_BUILD_EXAMPLES=OFF \
    -DLLAMA_BUILD_SERVER=OFF

echo "==> Building (this takes a few minutes)..."
cmake --build "${OUT_DIR}/build" --config Release -j"$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)"

echo "==> Copying headers and libraries..."
mkdir -p "${OUT_DIR}/include" "${OUT_DIR}/lib"

# Headers — location varies across llama.cpp versions
for h in llama.h ggml.h; do
    found=$(find "${OUT_DIR}/src" -name "${h}" | head -1)
    if [ -n "${found}" ]; then
        cp "${found}" "${OUT_DIR}/include/"
    fi
done
if [ ! -f "${OUT_DIR}/include/llama.h" ]; then
    echo "ERROR: llama.h not found in ${OUT_DIR}/src — header copy failed" >&2
    exit 1
fi

# Static libraries — location varies across cmake configurations
for lib in libllama.a libggml.a libggml-cpu.a libggml-base.a; do
    found=$(find "${OUT_DIR}/build" -name "${lib}" | head -1)
    if [ -n "${found}" ]; then
        cp "${found}" "${OUT_DIR}/lib/"
        echo "    Copied ${lib}"
    fi
done

if [ ! -f "${OUT_DIR}/lib/libllama.a" ]; then
    echo "ERROR: libllama.a not found after build — cmake layout may have changed" >&2
    exit 1
fi

echo "==> Done. Libraries at ${OUT_DIR}/lib/"
ls -lh "${OUT_DIR}/lib/"
