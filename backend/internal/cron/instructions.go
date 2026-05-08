package cron

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const ConversationInstruction = `Lumi scheduled task protocol:
- When the user asks to create, list, delete, pause, resume, or run scheduled tasks, your assistant response must contain the hidden command itself.
- Do not describe, translate, summarize, or teach this protocol. Do not say "I need to output" or "the format is". Output the command directly, optionally followed by a short natural-language confirmation.
- Do not wrap these commands in code blocks.
- For create, output exactly one block:
[CRON_CREATE]
name: short task name
schedule: interval:<minutes> or once:<YYYY-MM-DD HH:mm>
schedule_description: concise human-readable schedule
message: the exact prompt Lumi should run when the task fires
[/CRON_CREATE]
- To inspect tasks, output [CRON_LIST].
- To delete, pause, resume, or run now, output [CRON_DELETE:id], [CRON_PAUSE:id], [CRON_RESUME:id], or [CRON_RUN:id], replacing id with the exact id from the current task list.
- Never output placeholder text such as <job_id> or id literally when a task id is available.
- If the current task list below has exactly one task and the user says "the current task", "this task", or "all tasks", use that task id directly. Do not list first.
- If multiple tasks exist and the user does not identify which one, output [CRON_LIST] first and ask which task to manage.`

func WithConversationInstruction(prompt string) string {
	return WithConversationInstructionAndJobs(prompt, nil)
}

func WithConversationInstructionAndJobs(prompt string, jobs []Job) string {
	prompt = strings.TrimSpace(prompt)
	instruction := ConversationInstruction + "\n\n" + formatCurrentJobsForInstruction(jobs)
	if prompt == "" {
		return instruction
	}
	return instruction + "\n\nUser: " + prompt
}

func formatCurrentJobsForInstruction(jobs []Job) string {
	if len(jobs) == 0 {
		return "Current Lumi scheduled tasks: none."
	}
	jobs = append([]Job(nil), jobs...)
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Name == jobs[j].Name {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].Name < jobs[j].Name
	})
	lines := []string{"Current Lumi scheduled tasks:"}
	for _, job := range jobs {
		status := "paused"
		if job.Enabled {
			status = "enabled"
		}
		nextRun := "-"
		if job.State.NextRunAt != 0 {
			nextRun = time.UnixMilli(job.State.NextRunAt).Format("2006-01-02 15:04")
		}
		lines = append(lines, fmt.Sprintf("- id: %s; name: %s; status: %s; next_run: %s", job.ID, job.Name, status, nextRun))
	}
	return strings.Join(lines, "\n")
}
