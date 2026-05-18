package wecom

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
)

type IMSendRequest struct {
	Type     string
	Text     string
	Path     string
	FileName string
	Caption  string
	ReqID    string
	ChatID   string
	ChatType string
	UserID   string
}

func (s *Service) SendIM(ctx context.Context, req IMSendRequest) error {
	rt := s.currentRuntime()
	if rt == nil {
		return errors.New("wecom websocket is not connected")
	}
	rctx := replyContext{
		ReqID:    strings.TrimSpace(req.ReqID),
		ChatID:   strings.TrimSpace(req.ChatID),
		ChatType: strings.TrimSpace(req.ChatType),
		UserID:   strings.TrimSpace(req.UserID),
	}
	switch strings.TrimSpace(req.Type) {
	case "text":
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return errors.New("text is required")
		}
		if rctx.ChatID != "" {
			return rt.Send(ctx, rctx, text)
		}
		return rt.Reply(ctx, rctx, text)
	case "image", "file":
		action := SendAction{
			Type:         strings.TrimSpace(req.Type),
			Path:         strings.TrimSpace(req.Path),
			ResolvedPath: strings.TrimSpace(req.Path),
			FileName:     strings.TrimSpace(req.FileName),
			Caption:      strings.TrimSpace(req.Caption),
		}
		if action.FileName == "" {
			action.FileName = filepath.Base(action.Path)
		}
		if action.Type == "image" {
			if err := validateIMImage(action.Path); err != nil {
				return err
			}
		}
		if action.Caption != "" {
			if rctx.ChatID != "" {
				if err := rt.Send(ctx, rctx, action.Caption); err != nil {
					return err
				}
			} else if err := rt.Reply(ctx, rctx, action.Caption); err != nil {
				return err
			}
		}
		if rctx.ChatID != "" {
			return rt.SendMedia(ctx, rctx, action)
		}
		return rt.ReplyMedia(ctx, rctx, action)
	default:
		return errors.New("type must be text, image, or file")
	}
}

func validateIMImage(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return errors.New("image file is invalid: " + err.Error())
	}
	return nil
}
