package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/drellabot/orchestrator/internal/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPPort is the default port for the MCP server.
const MCPPort = 19090

// CodePuller pulls committed code from a sandbox into a local git repo.
type CodePuller interface {
	Pull(ctx context.Context, name, remotePath, localRepoDir string) error
}

// PullCodeInput is the input schema for the pull_code tool.
type PullCodeInput struct {
	Path string `json:"path" jsonschema_description:"Absolute path to the git repo in the sandbox"`
}

// Server wraps an MCP server that exposes the pull_code tool.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
}

// New creates a new MCP server. The pull_code tool calls puller.Pull to fetch
// committed code from the sandbox into the local task directory.
func New(logger *slog.Logger, taskName string, taskDir *task.Dir, puller CodePuller) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "orchestrator",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "pull_code",
		Description: "Pull committed code from the sandbox git repo to the host",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input *PullCodeInput) (*mcp.CallToolResult, any, error) {
		logger.Info("Code pull requested", "task", taskName, "path", input.Path)

		if err := puller.Pull(ctx, taskName, input.Path, taskDir.RepoPath()); err != nil {
			logger.Error("Code pull failed", "task", taskName, "error", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("pull_code failed: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		logger.Info("Code pulled", "task", taskName)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Code pulled successfully to host"},
			},
		}, nil, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	return &Server{
		httpServer: &http.Server{
			Handler: handler,
		},
	}
}

// Start starts the MCP server on 127.0.0.1:MCPPort.
func (s *Server) Start() error {
	return s.StartOn(fmt.Sprintf("127.0.0.1:%d", MCPPort))
}

// StartOn starts the MCP server on the given address.
func (s *Server) StartOn(addr string) error {
	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("MCP server error", "error", err)
		}
	}()

	return nil
}

// Addr returns the listener's address (useful when started on :0).
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Stop gracefully shuts down the MCP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
