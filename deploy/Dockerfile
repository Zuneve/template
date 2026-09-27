# syntax=docker/dockerfile:1

# builder содержит весь тулчейн Go и исходники; используется и для сборки
# бинаря, и (через --target=builder) как окружение для goose при накате
# миграций — эти два применения не касаются итогового образа сервиса.
FROM golang:1.27-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd cmd
COPY internal internal
COPY contracts contracts
COPY migrations migrations

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/trip-service ./cmd/trip-service

# Итоговый образ: distroless static, без shell, package manager, исходников и
# кеша сборки — только бинарь и root-сертификаты. Тег :nonroot уже запускает
# процесс под непривилегированным пользователем (uid 65532); USER продублирован
# явно, чтобы это было видно из Dockerfile, а не только из базового образа.
FROM gcr.io/distroless/static-debian12:nonroot AS final

COPY --from=builder /out/trip-service /trip-service

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/trip-service"]
