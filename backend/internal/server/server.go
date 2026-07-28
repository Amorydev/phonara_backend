// Package server sets up the Echo HTTP server with all middleware and routes.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	goredis "github.com/redis/go-redis/v9"
	echoswagger "github.com/swaggo/echo-swagger"

	_ "github.com/phonara/backend/docs" // swagger docs
	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/handler"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"

	"github.com/hibiken/asynq"

	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/service"
)

// Server wraps Echo and all wired dependencies.
type Server struct {
	echo       *echo.Echo
	cfg        *config.Config
	db         *pgxpool.Pool
	redis      *goredis.Client
	jwtMgr     *jwtutil.Manager
	audioStore storage.Store
	enqueue    *asynq.Client
	gate       *service.PracticeGate
}

// New creates a configured Echo server with all middleware applied.
func New(
	cfg *config.Config,
	db *pgxpool.Pool,
	rdb *goredis.Client,
	jwtMgr *jwtutil.Manager,
	audioStore storage.Store,
	enqueue *asynq.Client,
	gate *service.PracticeGate,
) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = custmiddleware.ErrorHandler
	e.Server.ReadHeaderTimeout = 5 * time.Second
	e.Server.ReadTimeout = 20 * time.Second
	e.Server.WriteTimeout = 35 * time.Second
	e.Server.IdleTimeout = 60 * time.Second

	// Global middleware
	e.Use(echomiddleware.Recover())
	e.Use(custmiddleware.RequestLogger())
	e.Use(echomiddleware.BodyLimit("3M"))
	e.Use(echomiddleware.Secure())
	e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins: cfg.Server.AllowedOrigins,
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization},
	}))
	e.Use(echomiddleware.RequestID())

	s := &Server{
		echo: e, cfg: cfg, db: db, redis: rdb, jwtMgr: jwtMgr,
		audioStore: audioStore, enqueue: enqueue, gate: gate,
	}
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

	if s.cfg.App.Env != "production" {
		e.GET("/swagger/*", echoswagger.WrapHandler)
	}

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
	jwtMiddleware := custmiddleware.JWT(s.jwtMgr, s.db)

	me := v1.Group("/me", jwtMiddleware)
	meHandler := handler.NewMeHandler(s.db, s.audioStore)
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
	sessionHandler := handler.NewSessionHandler(s.db, s.enqueue)
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

	// Onboarding Pre-Assessment
	assessmentHandler := handler.NewAssessmentHandler(s.db)
	assessments := v1.Group("/assessments", jwtMiddleware)
	assessments.GET("/pre-assessment", assessmentHandler.GetPreAssessment)

	// Chấm phát âm bất đồng bộ: upload → job → poll (§ đảo chiều luồng audio)
	assessmentJobHandler := handler.NewAssessmentJobHandler(s.db, s.audioStore, s.enqueue, s.gate)
	assessments.POST("", assessmentJobHandler.Create)
	assessments.GET("/:id", assessmentJobHandler.Get)

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
	subHandler := handler.NewSubscriptionHandler(s.db, s.redis, s.cfg)
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
	daily.GET("/challenges/:id", dailyHandler.GetChallenge)
	daily.GET("/history", dailyHandler.History)
	daily.GET("/mission", dailyHandler.GetMission)
	daily.POST("/mission/heartbeat", dailyHandler.Heartbeat)

	// Home aggregate (one call on Home entry)
	v1.GET("/home", handler.NewHomeHandler(s.db).Get, jwtMiddleware)

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
	// Audio mẫu là nội dung công khai, không phải dữ liệu người dùng → không cần token.
	// Bản ghi của người dùng KHÔNG phục vụ qua đây.
	v1.GET("/media/*", handler.NewMediaHandler(s.audioStore).Get)
	v1.GET("/app-config", sysHandler.AppConfig)
	v1.GET("/legal/:doc_type", sysHandler.Legal)
	v1.POST("/events", sysHandler.IngestAnalytics, jwtMiddleware)
}

// Echo returns the underlying Echo instance (for testing).
func (s *Server) Echo() *echo.Echo { return s.echo }
