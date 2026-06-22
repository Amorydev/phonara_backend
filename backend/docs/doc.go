// Package docs defines Swagger metadata for Phonara API.
//
// Phonara Backend API — Pronunciation Assessment & Learning Platform
//
// @title           Phonara API
// @version         1.0
// @description     Backend API cho ứng dụng luyện phát âm tiếng Anh Phonara.
// @description     Bao gồm: Authentication, Practice Sessions, Error Profile Engine,
// @description     Content, Shadowing, Minimal Pairs, Subscription, Exam (Speechace), Daily Challenge.
//
// @contact.name    Phonara Team
// @contact.email   dev@phonara.app
//
// @license.name    Proprietary
//
// @host            localhost:8080
// @BasePath        /v1
// @schemes         http https
//
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 JWT access token. Format: "Bearer <token>"
//
// @tag.name        Auth
// @tag.description Đăng ký, đăng nhập, refresh token, guest
//
// @tag.name        Me
// @tag.description Profile, notification preferences, privacy, account
//
// @tag.name        Speech
// @tag.description Token broker cho Azure Speech / Speechace
//
// @tag.name        Sessions
// @tag.description Practice session lifecycle và PA result ingestion
//
// @tag.name        Content
// @tag.description Words, sentences, minimal pairs, passages, fix guides
//
// @tag.name        Coach
// @tag.description Error Profile, recommendation, progress report
//
// @tag.name        Shadowing
// @tag.description Shadowing passage progress
//
// @tag.name        MinimalPairs
// @tag.description Listen drill (nghe phân biệt âm)
//
// @tag.name        Progress
// @tag.description Overview, charts, streak, badges
//
// @tag.name        Subscription
// @tag.description IAP, freemium quota, pricing plans
//
// @tag.name        Daily
// @tag.description Daily Challenge
//
// @tag.name        Exam
// @tag.description Exam Mode (IELTS/TOEIC) — server-side Speechace scoring
//
// @tag.name        System
// @tag.description Feedback, app config, legal docs, analytics events
//
// @tag.name        Probes
// @tag.description Health và readiness probes
package docs
