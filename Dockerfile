FROM golang:1.22-bookworm AS llama-builder

RUN apt-get update && apt-get install -y \
    build-essential \
    git \
    cmake \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

RUN git clone --depth 1 https://github.com/ggerganov/llama.cpp.git

WORKDIR /build/llama.cpp

RUN cmake -B build \
    -DCMAKE_BUILD_TYPE=Release \
    -DLLAMA_BUILD_SERVER=OFF \
    -DLLAMA_BUILD_EXAMPLES=OFF \
    -DLLAMA_CURL=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    && cmake --build build --config Release -j$(nproc)

FROM golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y \
    build-essential \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=llama-builder /build/llama.cpp/build/src/libllama.a /usr/local/lib/
COPY --from=llama-builder /build/llama.cpp/build/ggml/src/libggml.a /usr/local/lib/
COPY --from=llama-builder /build/llama.cpp/build/ggml/src/ggml-cpu/libggml-cpu.a /usr/local/lib/
COPY --from=llama-builder /build/llama.cpp/include /usr/local/include/llama
COPY --from=llama-builder /build/llama.cpp/ggml/include /usr/local/include/ggml
COPY --from=llama-builder /build/llama.cpp/src /usr/local/include/llama-src

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/usr/local/include/llama -I/usr/local/include/ggml -I/usr/local/include/llama-src"
ENV CGO_LDFLAGS="-L/usr/local/lib -lllama -lggml -lggml-cpu -lm -lpthread -lstdc++"

RUN go build -tags llama -o /agent ./cmd/agent

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    ca-certificates \
    libgomp1 \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /agent /app/agent

RUN mkdir -p /app/data /app/models

VOLUME ["/app/data", "/app/models"]

CMD ["./agent"]
