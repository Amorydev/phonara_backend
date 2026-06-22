package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// ContentHandler handles /v1/content endpoints.
type ContentHandler struct {
	svc *service.ContentService
}

// NewContentHandler creates a ContentHandler.
func NewContentHandler(db *pgxpool.Pool) *ContentHandler {
	return &ContentHandler{svc: service.NewContentService(db)}
}

// ListWords godoc
//
//	@Summary		Danh sách từ vựng
//	@Description	Lấy danh sách words active, filter theo topic/goal/phoneme (FR-WORD-07).
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			topic	query		string	false	"Topic (interview, travel…)"
//	@Param			goal	query		string	false	"Goal (communication, interview, ielts_toeic, beginner)"
//	@Param			phoneme	query		string	false	"Focus phoneme (vd: /θ/)"
//	@Success		200	{object}	domain.Response{data=[]service.ContentItem}	"Words list"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/content/words [get]
func (h *ContentHandler) ListWords(c echo.Context) error {
	_ = custmiddleware.UserIDFromCtx(c)
	topic := c.QueryParam("topic")
	goal := c.QueryParam("goal")
	phoneme := c.QueryParam("phoneme")

	items, err := h.svc.ListWords(ctxFromRequest(c), service.ContentFilter{
		Topic: topic, Goal: goal, Phoneme: phoneme,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// ListSentences godoc
//
//	@Summary		Danh sách câu luyện
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			topic	query		string	false	"Topic"
//	@Param			goal	query		string	false	"Goal"
//	@Success		200	{object}	domain.Response{data=[]service.ContentItem}	"Sentences list"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/content/sentences [get]
func (h *ContentHandler) ListSentences(c echo.Context) error {
	_ = custmiddleware.UserIDFromCtx(c)
	items, err := h.svc.ListSentences(ctxFromRequest(c), service.ContentFilter{
		Topic: c.QueryParam("topic"),
		Goal:  c.QueryParam("goal"),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// ListMinimalPairs godoc
//
//	@Summary		Danh sách minimal pairs
//	@Description	Lấy cặp âm tối thiểu, filter theo L1 error tag hoặc category chip (S15).
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			l1_tag		query		string	false	"L1 error tag code (final_consonant_deletion, th_sound…)"
//	@Param			category	query		string	false	"Category (final_sound, th, l_n, v_w, vowel_length)"
//	@Success		200	{object}	domain.Response{data=[]service.MinimalPairItem}	"Minimal pairs"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/content/minimal-pairs [get]
func (h *ContentHandler) ListMinimalPairs(c echo.Context) error {
	_ = custmiddleware.UserIDFromCtx(c)
	items, err := h.svc.ListMinimalPairs(ctxFromRequest(c), service.ContentFilter{
		L1Tag:    c.QueryParam("l1_tag"),
		Category: c.QueryParam("category"),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// ListPassages godoc
//
//	@Summary		Danh sách shadowing passages
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			source		query		string	false	"Source (curated, daily, news, dialogue)"
//	@Param			difficulty	query		string	false	"Difficulty level (1–5)"
//	@Success		200	{object}	domain.Response{data=[]service.PassageItem}	"Passages"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/content/passages [get]
func (h *ContentHandler) ListPassages(c echo.Context) error {
	_ = custmiddleware.UserIDFromCtx(c)
	items, err := h.svc.ListPassages(ctxFromRequest(c), service.ContentFilter{
		Source:     c.QueryParam("source"),
		Difficulty: c.QueryParam("difficulty"),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// ListPassageSentences godoc
//
//	@Summary		Câu trong passage
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string												true	"Passage UUID"
//	@Success		200	{object}	domain.Response{data=[]service.PassageSentenceItem}	"Sentences"
//	@Failure		401	{object}	domain.Response										"Unauthorized"
//	@Failure		404	{object}	domain.Response										"Passage not found"
//	@Router			/content/passages/{id}/sentences [get]
func (h *ContentHandler) ListPassageSentences(c echo.Context) error {
	items, err := h.svc.ListPassageSentences(ctxFromRequest(c), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// GetFixGuide godoc
//
//	@Summary		Fix guide hướng dẫn sửa lỗi phát âm
//	@Description	Trả fix guide tiếng Việt cho phoneme/l1_tag. Fallback về simplified guide nếu chưa có rich content (BR-L1-02).
//	@Tags			Content
//	@Produce		json
//	@Security		BearerAuth
//	@Param			phoneme	query		string	false	"IPA phoneme (vd: /θ/)"
//	@Param			l1_tag	query		string	false	"L1 error tag code"
//	@Success		200	{object}	domain.Response{data=service.FixGuideItem}	"Fix guide"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Not found"
//	@Router			/content/fix-guide [get]
func (h *ContentHandler) GetFixGuide(c echo.Context) error {
	guide, err := h.svc.GetFixGuide(ctxFromRequest(c), service.ContentFilter{
		Phoneme: c.QueryParam("phoneme"),
		L1Tag:   c.QueryParam("l1_tag"),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(guide))
}
