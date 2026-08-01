package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/imfile"
	"github.com/pengmide/lumi/internal/wecom"
)

type imSender interface {
	SendIM(context.Context, wecom.IMSendRequest) error
}

type imSendRequest struct {
	Channel        string             `json:"channel"`
	Type           string             `json:"type"`
	Text           string             `json:"text"`
	Path           string             `json:"path"`
	Caption        string             `json:"caption"`
	WorkspaceID    string             `json:"workspaceId"`
	WorkspacePath  string             `json:"workspacePath"`
	ConversationID string             `json:"conversationId"`
	WeCom          imSendWeComRequest `json:"wecom"`
}

type imSendWeComRequest struct {
	ReqID    string `json:"reqId"`
	ChatID   string `json:"chatId"`
	ChatType string `json:"chatType"`
	UserID   string `json:"userId"`
}

func (s *Server) handleIMSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req imSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := s.sendIM(r.Context(), req); err != nil {
		writeError(w, err.Error(), imSendStatus(err))
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) sendIM(ctx context.Context, req imSendRequest) error {
	channel := strings.TrimSpace(req.Channel)
	if channel != "wecom" {
		return imSendBadRequest("channel must be wecom")
	}
	typ := strings.TrimSpace(req.Type)
	if typ != "text" && typ != "image" && typ != "file" {
		return imSendBadRequest("type must be text, image, or file")
	}
	if strings.TrimSpace(req.WeCom.ChatID) == "" && strings.TrimSpace(req.WeCom.ReqID) == "" {
		if target, ok := s.currentIMTarget(lumicron.ChannelWeCom, req.ConversationID); ok && target.WeCom != nil {
			req.WeCom = imSendWeComRequest{
				ReqID: target.WeCom.ReqID, ChatID: target.WeCom.ChatID,
				ChatType: target.WeCom.ChatType, UserID: target.WeCom.UserID,
			}
		}
	}
	if strings.TrimSpace(req.WeCom.ChatID) == "" && strings.TrimSpace(req.WeCom.ReqID) == "" {
		return imSendBadRequest("wecom.chatId or wecom.reqId is required")
	}
	sender := s.wecomSender()
	if sender == nil {
		return imSendUnavailable("wecom service is unavailable")
	}
	sendReq := wecom.IMSendRequest{
		Type:     typ,
		Text:     req.Text,
		Caption:  req.Caption,
		ReqID:    req.WeCom.ReqID,
		ChatID:   req.WeCom.ChatID,
		ChatType: req.WeCom.ChatType,
		UserID:   req.WeCom.UserID,
	}
	if typ == "image" || typ == "file" {
		resolved, err := s.resolveIMSendFile(req)
		if err != nil {
			return err
		}
		sendReq.Path = resolved.Path
		sendReq.FileName = filepath.Base(resolved.Path)
	}
	if err := sender.SendIM(ctx, sendReq); err != nil {
		return imSendUnavailable(err.Error())
	}
	return nil
}

func (s *Server) wecomSender() imSender {
	if s.wecomIMSender != nil {
		return s.wecomIMSender
	}
	if s.wecom != nil {
		return s.wecom
	}
	return nil
}

func (s *Server) resolveIMSendFile(req imSendRequest) (imfile.ResolvedFile, error) {
	workspaceRoot := ""
	if strings.TrimSpace(req.WorkspaceID) != "" && s.config != nil {
		if ws := s.config.FindWorkspace(strings.TrimSpace(req.WorkspaceID)); ws != nil {
			workspaceRoot = ws.Path
		}
	}
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(req.WorkspacePath)
	}
	if workspaceRoot == "" {
		return imfile.ResolvedFile{}, imSendBadRequest("workspacePath is required")
	}
	resolved, reason := imfile.ResolveWorkspaceFile(req.Path, workspaceRoot)
	if reason != "" {
		return imfile.ResolvedFile{}, imSendBadRequest(reason)
	}
	if resolved.Info.Size() <= 5 {
		return imfile.ResolvedFile{}, imSendBadRequest("file must be larger than 5 bytes")
	}
	if resolved.Info.Size() > 20<<20 {
		return imfile.ResolvedFile{}, imSendBadRequest("file exceeds 20MB")
	}
	return resolved, nil
}

type imSendHTTPError struct {
	status int
	err    error
}

func (e imSendHTTPError) Error() string {
	return e.err.Error()
}

func (e imSendHTTPError) Unwrap() error {
	return e.err
}

func imSendBadRequest(message string) error {
	return imSendHTTPError{status: http.StatusBadRequest, err: errors.New(message)}
}

func imSendUnavailable(message string) error {
	return imSendHTTPError{status: http.StatusServiceUnavailable, err: errors.New(message)}
}

func imSendStatus(err error) int {
	var httpErr imSendHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status
	}
	return http.StatusInternalServerError
}
