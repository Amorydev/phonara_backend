// Package server sets up the Echo HTTP server with all middleware and routes.
package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	goredis "github.com/redis/go-redis/v9"
	echoswagger "github.com/swaggo/echo-swagger"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/handler"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	_ "github.com/phonara/backend/docs" // swagger docs
)

// Server wraps Echo and all wired dependencies.
type Server struct {
	echo   *echo.Echo
	cfg    *config.Config
	db     *pgxpool.Pool
	redis  *goredis.Client
	jwtMgr *jwtutil.Manager
}

// New creates a configured Echo server with all middleware applied.
func New(
	cfg *config.Config,
	db *pgxpool.Pool,
	rdb *goredis.Client,
	jwtMgr *jwtutil.Manager,
) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = custmiddleware.ErrorHandler

	// Global middleware
	e.Use(echomiddleware.Recover())
	e.Use(custmiddleware.RequestLogger())
	e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization},
	}))
	e.Use(echomiddleware.RequestID())

	s := &Server{echo: e, cfg: cfg, db: db, redis: rdb, jwtMgr: jwtMgr}
	s.registerRoutes()
	return s
}

// Start runs the HTTP server.
func (s *Server) Start() error {
	return s.echo.Start(s.cfg.Server.Addr())
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}

func (s *Server) registerRoutes() {
	e := s.echo

	// Probes (no auth)
	e.GET("/health", handler.Health)
	e.GET("/ready", handler.Ready(s.db, s.redis))

	// Swagger UI (development only — disable in production via APP_ENV check if needed)
	e.GET("/swagger/*", echoswagger.WrapHandler)

	// API v1
	v1 := e.Group("/v1")

	// Auth (public)
	authHandler := handler.NewAuthHandler(s.jwtMgr, s.db, s.redis)
	auth := v1.Group("/auth")
	auth.POST("/register", authHandler.Register)
	auth.POST("/login", authHandler.Login)
	auth.POST("/refresh", authHandler.Refresh)
	auth.POST("/guest", authHandler.Guest)

	// Protected routes
	jwtMiddleware := custmiddleware.JWT(s.jwtMgr)

	me := v1.Group("/me", jwtMiddleware)
	meHandler := handler.NewMeHandler(s.db)
	me.GET("", meHandler.GetMe)
	me.PATCH("", meHandler.UpdateMe)
	me.DELETE("", meHandler.DeleteMe)
	me.POST("/sync", meHandler.Sync)
	me.GET("/notifications", meHandler.GetNotifications)
	me.PATCH("/notifications", meHandler.UpdateNotifications)
	me.POST("/devices", meHandler.RegisterDevice)
	me.GET("/privacy", meHandler.GetPrivacy)
	me.PATCH("/privacy", meHandler.UpdatePrivacy)
	me.POST("/export", meHandler.ExportData)
	me.DELETE("/history", meHandler.DeleteHistory)

	// Speech gateway
	speechHandler := handler.NewSpeechHandler(s.db, s.redis, s.cfg)
	speech := v1.Group("/speech", jwtMiddleware)
	speech.POST("/token", speechHandler.IssueToken)

	// Practice sessions
	sessionHandler := handler.NewSessionHandler(s.db, s.redis, s.cfg)
	sessions := v1.Group("/sessions", jwtMiddleware)
	sessions.POST("", sessionHandler.Create)
	sessions.GET("/history", sessionHandler.History)
	sessions.GET("/in-progress", sessionHandler.InProgress)
	sessions.GET("/:id", sessionHandler.Get)
	sessions.POST("/:id/end", sessionHandler.End)
	sessions.POST("/:id/results", sessionHandler.IngestResult)
	sessions.POST("/:id/results:batch", sessionHandler.IngestBatch)

	// Content
	contentHandler := handler.NewContentHandler(s.db)
	content := v1.Group("/content", jwtMiddleware)
	content.GET("/words", contentHandler.ListWords)
	content.GET("/sentences", contentHandler.ListSentences)
	content.GET("/minimal-pairs", contentHandler.ListMinimalPairs)
	content.GET("/passages", contentHandler.ListPassages)
	content.GET("/passages/:id/sentences", contentHandler.ListPassageSentences)
	content.GET("/fix-guide", contentHandler.GetFixGuide)

	// Coach / Error Profile
	coachHandler := handler.NewCoachHandler(s.db, s.redis)
	coach := v1.Group("/coach", jwtMiddleware)
	coach.GET("/profile", coachHandler.GetProfile)
	coach.GET("/recommendation", coachHandler.GetRecommendation)
	coach.GET("/report", coachHandler.GetReport)

	// Shadowing
	shadowHandler := handler.NewShadowingHandler(s.db)
	shad := v1.Group("/shadowing", jwtMiddleware)
	shad.GET("/:passage_id/progress", shadowHandler.GetProgress)
	shad.POST("/:passage_id/sentence-result", shadowHandler.SubmitSentenceResult)
	shad.POST("/:passage_id/complete", shadowHandler.Complete)

	// Minimal Pairs listen drill
	mpHandler := handler.NewMinimalPairHandler(s.db, s.redis)
	mp := v1.Group("/minimal-pairs", jwtMiddleware)
	mp.POST("/listen/start", mpHandler.StartListenDrill)
	mp.POST("/listen/:drill_id/answer", mpHandler.SubmitAnswer)
	mp.GET("/listen/:drill_id", mpHandler.GetDrillStatus)

	// Progress & Streak
	progressHandler := handler.NewProgressHandler(s.db)
	progress := v1.Group("/progress", jwtMiddleware)
	progress.GET("/overview", progressHandler.Overview)
	progress.GET("/charts", progressHandler.Charts)

	v1.GET("/badges", handler.NewBadgeHandler(s.db).List, jwtMiddleware)
	v1.POST("/streak/check-in", handler.NewStreakHandler(s.db, s.redis, s.cfg).CheckIn, jwtMiddleware)

	// Subscription & IAP
	subHandler := handler.NewSubscriptionHandler(s.db, s.cfg)
	sub := v1.Group("/subscription", jwtMiddleware)
	sub.GET("", subHandler.Get)
	sub.GET("/plans", subHandler.Plans)
	sub.POST("/verify", subHandler.Verify)
	sub.POST("/restore", subHandler.Restore)
	v1.POST("/iap/webhook/apple", subHandler.WebhookApple)
	v1.POST("/iap/webhook/google", subHandler.WebhookGoogle)
	v1.GET("/freemium/quota", subHandler.Quota, jwtMiddleware)

	// Daily Challenge
	dailyHandler := handler.NewDailyHandler(s.db)
	daily := v1.Group("/daily", jwtMiddleware)
	daily.GET("/today", dailyHandler.Today)
	daily.GET("/history", dailyHandler.History)

	// Exam
	examHandler := handler.NewExamHandler(s.db, s.cfg)
	exam := v1.Group("/exam", jwtMiddleware)
	exam.GET("/prompts", examHandler.ListPrompts)
	exam.POST("/sessions", examHandler.Create)
	exam.POST("/sessions/:id/submit", examHandler.Submit)
	exam.GET("/sessions/:id/report", examHandler.Report)
	exam.GET("/sessions", examHandler.ListSessions)

	// System
	sysHandler := handler.NewSystemHandler(s.db)
	v1.POST("/feedback", sysHandler.Feedback, jwtMiddleware)
	v1.GET("/app-config", sysHandler.AppConfig)
	v1.GET("/legal/:doc_type", sysHandler.Legal)
	v1.POST("/events", sysHandler.IngestAnalytics, jwtMiddleware)
}

// Echo returns the underlying Echo instance (for testing).
func (s *Server) Echo() *echo.Echo { return s.echo }

// healthResponse is the ready probe response.
type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db,omitempty"`
	Redis  string `json:"redis,omitempty"`
}

// readyCheck checks DB and Redis connectivity.
func readyCheck(ctx context.Context, db *pgxpool.Pool, rdb *goredis.Client) healthResponse {
	resp := healthResponse{Status: "ok"}

	if err := db.Ping(ctx); err != nil {
		resp.Status = "degraded"
		resp.DB = fmt.Sprintf("unhealthy: %s", err.Error())
	} else {
		resp.DB = "ok"
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		resp.Status = "degraded"
		resp.Redis = fmt.Sprintf("unhealthy: %s", err.Error())
	} else {
		resp.Redis = "ok"
	}

	return resp
}

// These are needed here to avoid import cycles but the actual handlers are in handler package.
var _ = domain.OK // ensure domain is imported
