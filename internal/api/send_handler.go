package api

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"xmail/internal/mailer"
)

type attachmentRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	// DataBase64 is the attachment content, base64-encoded (JSON has no
	// native binary type).
	DataBase64 string `json:"data_base64"`
}

type sendRequest struct {
	To          []string            `json:"to"`
	CC          []string            `json:"cc"`
	BCC         []string            `json:"bcc"`
	Subject     string              `json:"subject"`
	BodyText    string              `json:"body_text"`
	BodyHTML    string              `json:"body_html"`
	Attachments []attachmentRequest `json:"attachments"`
	// Headers are additional custom header lines (e.g. "X-Priority",
	// "Reply-To") — see PRD.MD §6.2 "custom headers dasar".
	Headers map[string]string `json:"headers"`
}

func (r sendRequest) toDomain() (mailer.OutgoingMessage, error) {
	msg := mailer.OutgoingMessage{
		To:       r.To,
		CC:       r.CC,
		BCC:      r.BCC,
		Subject:  r.Subject,
		BodyText: r.BodyText,
		BodyHTML: r.BodyHTML,
		Headers:  r.Headers,
	}
	for _, a := range r.Attachments {
		data, err := base64.StdEncoding.DecodeString(a.DataBase64)
		if err != nil {
			return mailer.OutgoingMessage{}, fmt.Errorf("attachment %q: invalid base64 data: %w", a.Filename, err)
		}
		msg.Attachments = append(msg.Attachments, mailer.Attachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Data:        data,
		})
	}
	return msg, nil
}

// handleSend implements POST /accounts/{id}/send (see PLAN.md §3).
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeJSON[sendRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(req.To) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "to is required")
		return
	}
	msg, err := req.toDomain()
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	if err := s.service.Send(r.Context(), id, msg); err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"status": "sent"})
}
