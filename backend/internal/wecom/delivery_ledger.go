package wecom

import (
	"crypto/sha1"
	"fmt"
	"strings"
)

type DeliveryStatus string

const (
	DeliveryStatusPending   DeliveryStatus = "pending"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusFailed    DeliveryStatus = "failed"
	DeliveryStatusSkipped   DeliveryStatus = "skipped"
)

type DeliveryMethod string

const (
	DeliveryMethodStream DeliveryMethod = "stream"
	DeliveryMethodSend   DeliveryMethod = "send"
	DeliveryMethodMedia  DeliveryMethod = "media"
)

type DeliveredUnit struct {
	ID             string
	SourceType     string
	RenderedKind   string
	Text           string
	Action         *SendAction
	ContentHash    string
	StreamID       string
	DeliveryMethod DeliveryMethod
	Status         DeliveryStatus
	Error          string
}

type DeliveryLedger struct {
	units []DeliveredUnit
}

func NewDeliveryLedger() *DeliveryLedger {
	return &DeliveryLedger{}
}

func (l *DeliveryLedger) Add(unit DeliveredUnit) int {
	if l == nil {
		return -1
	}
	if unit.ID == "" {
		unit.ID = fmt.Sprintf("unit-%d", len(l.units)+1)
	}
	if unit.Status == "" {
		unit.Status = DeliveryStatusPending
	}
	if unit.ContentHash == "" {
		unit.ContentHash = deliveryContentHash(unit)
	}
	l.units = append(l.units, unit)
	return len(l.units) - 1
}

func (l *DeliveryLedger) Mark(index int, status DeliveryStatus, err error) {
	if l == nil || index < 0 || index >= len(l.units) {
		return
	}
	l.units[index].Status = status
	if err != nil {
		l.units[index].Error = err.Error()
	} else {
		l.units[index].Error = ""
	}
}

func (l *DeliveryLedger) MarkByID(id string, status DeliveryStatus, err error) bool {
	if l == nil {
		return false
	}
	for i := range l.units {
		if l.units[i].ID == id {
			l.Mark(i, status, err)
			return true
		}
	}
	return false
}

func (l *DeliveryLedger) Units() []DeliveredUnit {
	if l == nil {
		return nil
	}
	out := make([]DeliveredUnit, len(l.units))
	copy(out, l.units)
	return out
}

func (l *DeliveryLedger) PendingOrFailedUnits() []DeliveredUnit {
	if l == nil {
		return nil
	}
	out := make([]DeliveredUnit, 0, len(l.units))
	for _, unit := range l.units {
		if unit.Status == DeliveryStatusPending || unit.Status == DeliveryStatusFailed {
			out = append(out, unit)
		}
	}
	return out
}

func (l *DeliveryLedger) PendingOrFailedIndexes() []int {
	if l == nil {
		return nil
	}
	out := make([]int, 0, len(l.units))
	for i, unit := range l.units {
		if unit.Status == DeliveryStatusPending || unit.Status == DeliveryStatusFailed {
			out = append(out, i)
		}
	}
	return out
}

func (l *DeliveryLedger) Valid() bool {
	if l == nil || len(l.units) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, unit := range l.units {
		if strings.TrimSpace(unit.ID) == "" || unit.Status == "" || unit.DeliveryMethod == "" || unit.ContentHash == "" {
			return false
		}
		if _, ok := seen[unit.ID]; ok {
			return false
		}
		seen[unit.ID] = struct{}{}
	}
	return true
}

func deliveryContentHash(unit DeliveredUnit) string {
	source := unit.Text
	if unit.Action != nil {
		source = unit.Action.Type + "\x00" + unit.Action.Path + "\x00" + unit.Action.Caption
	}
	sum := sha1.Sum([]byte(unit.SourceType + "\x00" + unit.RenderedKind + "\x00" + source))
	return fmt.Sprintf("%x", sum[:])
}
