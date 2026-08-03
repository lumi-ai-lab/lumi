package piruntime

const (
	SandboxHome             = "/lumi/pi-home"
	SandboxAgentDir         = SandboxHome + "/.pi/agent"
	SandboxPiCommand        = "/lumi/runtime/npm/bin/pi"
	SandboxCredentialSource = "/lumi/pi-credential-source"
	EnvPiAgentDir           = "PI_CODING_AGENT_DIR"
	EnvPiCommand            = "PI_ACP_PI_COMMAND"
	EnvPiCredentialSource   = "LUMI_PI_CREDENTIAL_SOURCE"
)
