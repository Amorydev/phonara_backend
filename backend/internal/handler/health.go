// Package handler contains all HTTP handlers (thin layer: bind → validate → service → JSON).
package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/domain"
)

// Health godoc
//
//	@Summary		Liveness probe
//	@Description	Luôn trả 200 nếu process còn sống. Dùng cho Docker/K8s liveness check.
//	@Tags			Probes
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=domain.StatusData}	"ok"
//	@Router			/health [get]
func Health(c echo.Context) error {
	return c.JSON(http.StatusOK, domain.OK(map[string]string{"status": "ok"}))
}

// Ready godoc
//
//	@Summary		Readiness probe
//	@Description	Kiểm tra kết nối DB và Redis. Trả 503 nếu một trong hai không kết nối được.
//	@Tags			Probes
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=domain.StatusData}	"all healthy"
//	@Failure		503	{object}	domain.Response{data=domain.StatusData}	"degraded"
//	@Router			/ready [get]
func Ready(db *pgxpool.Pool, rdb *goredis.Client) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		resp := map[string]string{
			"status": "ok",
		}

		code := http.StatusOK

		if err := db.Ping(ctx); err != nil {
			resp["status"] = "degraded"
			resp["db"] = "unhealthy"
			slog.WarnContext(ctx, "readiness database check failed", "err", err)
			code = http.StatusServiceUnavailable
		} else {
			resp["db"] = "ok"
		}

		if err := rdb.Ping(ctx).Err(); err != nil {
			resp["status"] = "degraded"
			resp["redis"] = "unhealthy"
			slog.WarnContext(ctx, "readiness redis check failed", "err", err)
			code = http.StatusServiceUnavailable
		} else {
			resp["redis"] = "ok"
		}

		return c.JSON(code, domain.OK(resp))
	}
}

// bindAndValidate binds the request body and validates it using go-playground/validator.
func bindAndValidate(c echo.Context, req any) error {
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
	}
	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	return nil
}

// ctxFromRequest is a convenience helper to get the request context.
func ctxFromRequest(c echo.Context) context.Context {
	return c.Request().Context()
}
