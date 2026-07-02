# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## What this is

A personal learning project for the [CloudWeGo **eino**](https://github.com/cloudwego/eino) LLM application framework (Go). Code is organized as a series of numbered, self-contained examples under `examples/`, each demonstrating one eino concept. Comments are in Chinese and frequently draw analogies to FinTech systems (LP clients, RPC, SSE, settlement) — keep that explanatory, analogy-driven style when adding code.

## Setup & commands

Configuration is read from a `.env` file at the repo root (gitignored, contains the API key):

```bash
cp .env.example .env   # then fill in OPENAI_API_KEY
```

`internal/llm` calls `godotenv.Load()` in its package `init()`, so `.env` is loaded automatically as long as the process runs from the repo root. Real exported env vars take precedence over `.env`.

```bash
go run ./examples/01_chatmodel   # run an example (must be from repo root so .env is found)
go build ./...                   # build everything
go vet ./...                     # vet
```

Tests live in `internal/vectorstore`. Run them with:

```bash
go test ./...                                      # all tests
go test -v ./internal/vectorstore/                 # one package, verbose (scores are logged)
go test -v -run RealEmbedding ./internal/vectorstore/   # the one test that hits the real API
```

Most tests use a hand-rolled `fakeEmbedder` (word-count vectors) so they run offline and free. The `RealEmbedding` test actually calls the OpenAI embedding API and **self-skips when `OPENAI_API_KEY` is unset** — so a bare `go test ./...` is always safe and offline.

There is no Makefile. In VS Code, use the "Debug 01_chatmodel" or "Debug current package" launch configs (`.vscode/launch.json`) — both pin `cwd` to the repo root and inject `.env`.

## Architecture

- **`internal/llm/model.go`** — single chokepoint for "how to connect to a model." Two factories, both returning the **eino interface type** (not the concrete OpenAI struct) so callers depend only on the abstraction:
  - `NewChatModel(ctx)` builds a `model.BaseChatModel` from `OPENAI_API_KEY`, `OPENAI_BASE_URL` (defaults to the official API), and `OPENAI_MODEL` (defaults to `gpt-4o-mini`).
  - `NewEmbedder(ctx)` builds an `embedding.Embedder` from the same key/base URL plus `OPENAI_EMBEDDING_MODEL` (defaults to `text-embedding-3-small`). Chat and embedding are **different models on different endpoints** — separate model names, separately billed.

  All examples and packages should obtain their model/embedder through these factories rather than constructing one inline.

  Embedding-specific env vars (all optional, fall back to the chat equivalents):
  - `OPENAI_EMBEDDING_API_KEY` — embedding 专用 key；未设置则回退到 `OPENAI_API_KEY`
  - `OPENAI_EMBEDDING_BASE_URL` — embedding 专用网关；未设置则回退到 `OPENAI_BASE_URL`
  - `OPENAI_EMBEDDING_MODEL` — embedding 模型名 / 接入点 ID，默认 `text-embedding-3-small`
  - `OPENAI_EMBEDDING_BATCH` — 设为 `false`/`0`/`off` 可关闭批量 embedding（豆包等只收单条的接入点需要）。默认 `true`。关闭时 `NewEmbedder` 自动包一层 `perTextEmbedder` 适配器把批量拆成逐条，上层 `Store` 不用改。

  **Provider compatibility:** The code uses OpenAI-compatible endpoints throughout (no OpenAI-specific SDKs or features). Any provider that speaks the `/v1/chat/completions` and `/v1/embeddings` protocols works — just swap `OPENAI_BASE_URL`. The `.env.example` includes a full 火山方舟 (Volcengine Ark / 豆包 Doubao) config example. Key caveat: chat and embedding are separate services even on the same provider, each with its own endpoint/model/billing — mixing their credentials produces empty vectors or 404s.

- **`internal/vectorstore/store.go`** — a minimal in-memory vector store written from scratch (cosine similarity over `schema.Document` dense vectors) to make retrieval mechanics visible. It implements eino's `retriever.Retriever` interface, so to upper layers it's interchangeable with Qdrant/Milvus. It takes an `embedding.Embedder` as a dependency — the tests swap a fake embedder for the real one with **zero changes to `Store`**, which is the interface-substitution lesson the package exists to teach.

  Tests live in two files:
  - `store_test.go` (`package vectorstore`) — white-box tests with `fakeEmbedder` (word-count vectors, offline/free).
  - `store_realembedding_test.go` (`package vectorstore_test`) — black-box integration test that calls the real OpenAI embedding API and self-skips when `OPENAI_API_KEY` is unset.

- **`examples/NN_<topic>/main.go`** — each is its own `package main`. New examples follow the same numbering convention and reuse `internal/llm`.
  - `01_chatmodel` — the two core `ChatModel` call styles: `Generate` (blocking, full reply + token `Usage`) and `Stream` (returns a `StreamReader` you must `Close`, read in a `Recv()` loop until `io.EOF`).
  - `02_menu_agent` — **in progress**, no `main.go` yet. `data/history.json` is a toddler meal history (lunch/fruit/dinner per day) that will serve as the knowledge base for a menu-planning agent.
  - `03_embedding_internals` — deep-dive into embedding math: cosine vs Euclidean distance, why only direction matters for semantics, why length doesn't affect cosine, and the fact that OpenAI vectors are pre-normalized (|v|≈1, so cosine degenerates to dot product). Part A is pure math (offline), Part B pulls real OpenAI vectors to verify. Deliberately implements `norm()`, `dot()`, `unit()`, `cosine()`, `euclid()` from scratch in the example (no library) so the formulas are visible and verifiable — the same cosine implementation lives in `vectorstore/store.go`.

Conversations are built as `[]*schema.Message` using `schema.SystemMessage` / `schema.UserMessage`; an assistant reply is itself a `*schema.Message`.
