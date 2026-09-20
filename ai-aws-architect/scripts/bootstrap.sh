#!/usr/bin/env bash
# Resolves dependencies. Gin is pinned; everything else tracks latest.
set -euo pipefail

go get github.com/gin-gonic/gin@v1.12.0

go get github.com/gin-contrib/cors@latest
go get github.com/golang-jwt/jwt/v5@latest
go get github.com/google/uuid@latest
go get github.com/jackc/pgx/v5@latest
go get golang.org/x/crypto@latest
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime@latest
go get github.com/aws/aws-sdk-go-v2/service/sts@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest

go mod tidy
go build ./...

# Commit go.sum so everyone bootstrapping later gets the same tree.
