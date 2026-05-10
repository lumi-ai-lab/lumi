package cron

type Schedule struct {
	Type         string `json:"type"`
	CronExpr     string `json:"cronExpr,omitempty"`
	RunAt        int64  `json:"runAt,omitempty"`
	EverySeconds int64  `json:"everySeconds,omitempty"`
}

type JobState struct {
	LastRunAt  int64  `json:"lastRunAt,omitempty"`
	NextRunAt  int64  `json:"nextRunAt,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	RunCount   int    `json:"runCount"`
}

type Job struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Enabled        bool     `json:"enabled"`
	Channel        string   `json:"channel,omitempty"`
	WorkspaceID    string   `json:"workspaceId"`
	AgentID        string   `json:"agentId"`
	ConversationID string   `json:"conversationId,omitempty"`
	Schedule       Schedule `json:"schedule"`
	Prompt         string   `json:"prompt,omitempty"`
	Exec           string   `json:"exec,omitempty"`
	Silent         *bool    `json:"silent,omitempty"`
	Mute           bool     `json:"mute,omitempty"`
	SessionMode    string   `json:"sessionMode,omitempty"`
	WorkDir        string   `json:"workDir,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	TimeoutMins    *int     `json:"timeoutMins,omitempty"`
	Target         Target   `json:"target,omitempty"`
	State          JobState `json:"state"`
	CreatedAt      int64    `json:"createdAt"`
	UpdatedAt      int64    `json:"updatedAt"`
}

type Target struct {
	WeChat *WeChatTarget `json:"wechat,omitempty"`
	WeCom  *WeComTarget  `json:"wecom,omitempty"`
}

type WeChatTarget struct {
	ConversationKey string `json:"conversationKey,omitempty"`
	ContextToken    string `json:"contextToken,omitempty"`
}

type WeComTarget struct {
	ReqID    string `json:"reqId,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	ChatType string `json:"chatType,omitempty"`
	UserID   string `json:"userId,omitempty"`
}

type Event struct {
	Type           string `json:"type"`
	Job            *Job   `json:"job,omitempty"`
	JobID          string `json:"jobId,omitempty"`
	Channel        string `json:"channel,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	Event          string `json:"event,omitempty"`
	Data           any    `json:"data,omitempty"`
}

type Command struct {
	Kind                string
	Name                string
	Schedule            Schedule
	ScheduleDescription string
	Prompt              string
	JobID               string
}

type SkippedError struct {
	Reason string
}

func (e SkippedError) Error() string {
	if e.Reason == "" {
		return "skipped"
	}
	return e.Reason
}
