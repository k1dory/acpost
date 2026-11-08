package node

import acp "github.com/coder/acp-go-sdk"

type acpPromptRequest = acp.PromptRequest

func textBlocks(s string) []acp.ContentBlock { return []acp.ContentBlock{acp.TextBlock(s)} }

type acpSessionID = acp.SessionId
