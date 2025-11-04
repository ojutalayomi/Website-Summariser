package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// Gemini API endpoint
const geminiAPIEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-preview-09-2025:generateContent?key="

// Global API key variable
var apiKey string

// --- Structs for Gemini API Request ---

type GeminiRequest struct {
	Contents          []Content          `json:"contents"`
	Tools             []Tool             `json:"tools,omitempty"`
	SystemInstruction *SystemInstruction `json:"systemInstruction,omitempty"`
}

type Content struct {
	Parts []Part `json:"parts"`
}

type Part struct {
	Text string `json:"text"`
}

type Tool struct {
	GoogleSearch *GoogleSearch `json:"google_search"`
}

type GoogleSearch struct {
	// Empty struct signifies to use Google Search
}

type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

// --- Structs for Gemini API Response ---

type GeminiResponse struct {
	Candidates []Candidate `json:"candidates"`
}

type Candidate struct {
	Content           Content            `json:"content"`
	GroundingMetadata *GroundingMetadata `json:"groundingMetadata,omitempty"`
}

type GroundingMetadata struct {
	GroundingAttributions []GroundingAttribution `json:"groundingAttributions,omitempty"`
}

type GroundingAttribution struct {
	Web *WebAttribution `json:"web,omitempty"`
}

type WebAttribution struct {
	URI   string `json:"uri"`
	Title string `json:"title"`
}

// --- Structs for our REST API ---

type APIAgentRequest struct {
	URL string `json:"url" binding:"required"`
}

type APIAgentResponse struct {
	URL          string `json:"url"`
	Summary      string `json:"summary"`
	Alternatives string `json:"alternatives"`
}

// --- Structs for A2A JSON-RPC Communication ---

// JSON-RPC Request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	ID      interface{} `json:"id"`
	Params  *RPCParams  `json:"params"`
}

// JSON-RPC Response (Success)
type JSONRPCSuccessResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  *RPCResult  `json:"result"`
}

// JSON-RPC Response (Error)
type JSONRPCErrorResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Error   *RPCError   `json:"error"`
}

// RPC Params
type RPCParams struct {
	Message *RPCMessage `json:"message"`
}

// RPC Message
type RPCMessage struct {
	Role      string    `json:"role"`
	Parts     []RPCPart `json:"parts"`
	Kind      string    `json:"kind,omitempty"`
	MessageID string    `json:"messageId,omitempty"`
}

// RPC Part
type RPCPart struct {
	Kind string         `json:"kind"`           // "text" or "data"
	Type string         `json:"type,omitempty"` // Deprecated, use "kind"
	Text string         `json:"text,omitempty"`
	Data []DataPartItem `json:"data,omitempty"` // For conversation history
}

// DataPartItem represents items in the data array (conversation history)
type DataPartItem struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// RPC Result
// RPC Result (A2A expects Task and Message objects)
type RPCResult struct {
	Task    RPCTask          `json:"Task"`
	Message RPCMessageResult `json:"Message"`
}

// RPCTask represents task metadata required by A2A
type RPCTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// RPCMessageResult represents the message payload in the result
type RPCMessageResult struct {
	Role      string    `json:"role"`
	Parts     []RPCPart `json:"parts"`
	Kind      string    `json:"kind"`
	MessageID string    `json:"messageId"`
}

// RPC Error
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Agent Card
type AgentCard struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Categories  []string `json:"categories"`
}

// --- Main Application (Server) ---

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	// 1. Load API Key from environment variable
	apiKey = os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY environment variable not set. Please set it before running.")
	}

	// 2. Set up Gin router
	// Set Gin to Release Mode to suppress debug output
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// A2A endpoints
	router.GET("/.well-known/agent.json", agentCardHandler)
	router.POST("/", jsonRPCHandler)

	// Legacy endpoint (backward compatibility)
	router.POST("/agent", agentHandler)

	// 3. Run the server
	port := "8080"
	log.Printf("Starting AI Agent API server on :%s\n", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// agentHandler handles requests to the /agent endpoint using Gin
func agentHandler(c *gin.Context) {
	var req APIAgentRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	log.Printf("Handling request for URL: %s\n", req.URL)

	// Run the agent logic
	summary, alternatives, err := runAgent(req.URL)
	if err != nil {
		log.Printf("Agent failed for %s: %v\n", req.URL, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process request: " + err.Error()})
		return
	}

	// Send the successful response
	resp := APIAgentResponse{
		URL:          req.URL,
		Summary:      summary,
		Alternatives: alternatives,
	}
	c.JSON(http.StatusOK, resp)
}

// runAgent orchestrates the fetch, summarize, and find-alternatives flow
func runAgent(url string) (string, string, error) {
	// 1. Fetch and extract website text (Must be synchronous first step)
	log.Println("Fetching and extracting content...")
	textContent, err := fetchAndExtract(url)
	if err != nil {
		return "", "", fmt.Errorf("could not fetch or extract content: %w", err)
	}
	if textContent == "" {
		return "", "", fmt.Errorf("no content extracted from %s", url)
	}
	log.Println("Content extracted successfully.")

	// Truncate text to a reasonable length for the API (e.g., 15k characters for summary)
	summaryText := textContent
	if len(summaryText) > 15000 {
		summaryText = string([]rune(summaryText)[:15000])
	}

	// For alternatives, we only need a smaller sample (first 5000 chars) to understand the site
	// This reduces payload size and processing time
	alternativesText := textContent
	if len(alternativesText) > 5000 {
		alternativesText = string([]rune(alternativesText)[:5000])
	}

	// Define channels to receive results from goroutines
	type APIResult struct {
		Result string
		Err    error
	}
	summaryChan := make(chan APIResult, 1)
	alternativesChan := make(chan APIResult, 1)

	// Create context with timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// 2. Start two independent tasks concurrently
	// Task 1: Get Summary
	log.Println("Starting summary generation (concurrent)...")
	go func() {
		s, err := getSummary(ctx, summaryText)
		summaryChan <- APIResult{s, err}
	}()

	// Task 2: Get Alternatives (now depends on textContent, not summary)
	log.Println("Starting alternatives search (concurrent)...")
	go func() {
		// NOTE: We pass a smaller text sample to reduce processing time
		a, err := getAlternatives(ctx, url, alternativesText)
		alternativesChan <- APIResult{a, err}
	}()

	// 3. Wait for results from both channels with context timeout
	select {
	case sumResult := <-summaryChan:
		if sumResult.Err != nil {
			return "", "", fmt.Errorf("could not generate summary: %w", sumResult.Err)
		}
		// Wait for alternatives
		select {
		case altResult := <-alternativesChan:
			if altResult.Err != nil {
				return "", "", fmt.Errorf("could not find alternatives: %w", altResult.Err)
			}
			log.Println("Agent run successful (concurrent).")
			return sumResult.Result, altResult.Result, nil
		case <-ctx.Done():
			return "", "", fmt.Errorf("timeout waiting for alternatives: %w", ctx.Err())
		}
	case altResult := <-alternativesChan:
		if altResult.Err != nil {
			return "", "", fmt.Errorf("could not find alternatives: %w", altResult.Err)
		}
		// Wait for summary
		select {
		case sumResult := <-summaryChan:
			if sumResult.Err != nil {
				return "", "", fmt.Errorf("could not generate summary: %w", sumResult.Err)
			}
			log.Println("Agent run successful (concurrent).")
			return sumResult.Result, altResult.Result, nil
		case <-ctx.Done():
			return "", "", fmt.Errorf("timeout waiting for summary: %w", ctx.Err())
		}
	case <-ctx.Done():
		return "", "", fmt.Errorf("timeout waiting for API responses: %w", ctx.Err())
	}
}

// fetchAndExtract uses http.Get and goquery to get the body text of a website
func fetchAndExtract(url string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	res, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return "", fmt.Errorf("request failed with status: %s", res.Status)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return "", err
	}

	// Remove script and style tags to clean up the text
	doc.Find("script, style").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	// Get text from the body
	bodyText := doc.Find("body").Text()

	// Clean up whitespace
	// Replace multiple newlines/spaces with a single space
	cleanedText := strings.Join(strings.Fields(bodyText), " ")

	return cleanedText, nil
}

// getSummary calls Gemini to summarize the extracted text
func getSummary(ctx context.Context, text string) (string, error) {
	systemPrompt := "Provide a concise one-paragraph summary focusing on main purpose and key offerings."
	userPrompt := fmt.Sprintf("Summarize this website content:\n\n%s", text)

	return callGemini(ctx, systemPrompt, userPrompt, false)
}

// getAlternatives is modified to take textContent for concurrency
func getAlternatives(ctx context.Context, url, textContent string) (string, error) {
	// The prompt is adjusted to use the provided content for context before searching
	// Using a more concise prompt to reduce processing time
	systemPrompt := "Find 3-5 alternative websites using Google Search. List each with URL and one-sentence description. Exclude the provided URL and its subdomains."
	userPrompt := fmt.Sprintf("Website: %s\nContent sample: %s\n\nFind similar alternatives.", url, textContent)

	return callGemini(ctx, systemPrompt, userPrompt, true)
}

// callGemini is the core function for making API requests
func callGemini(ctx context.Context, systemPrompt, userPrompt string, useSearch bool) (string, error) {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context cancelled: %w", ctx.Err())
	default:
	}

	fullURL := geminiAPIEndpoint + apiKey

	// 1. Build the request payload
	reqBody := GeminiRequest{
		SystemInstruction: &SystemInstruction{
			Parts: []Part{{Text: systemPrompt}},
		},
		Contents: []Content{
			{
				Parts: []Part{{Text: userPrompt}},
			},
		},
	}

	if useSearch {
		reqBody.Tools = []Tool{{GoogleSearch: &GoogleSearch{}}}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// 2. Make the request with exponential backoff
	var resp *http.Response
	maxRetries := 3                     // Reduced from 4 to 3 for faster failure
	baseDelay := 500 * time.Millisecond // Reduced initial delay

	// Use shorter timeout for individual requests, context handles overall timeout
	client := &http.Client{
		Timeout: 25 * time.Second,
	}

	for i := 0; i < maxRetries; i++ {
		// Check context before each retry
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		// Create request with context
		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(payload))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			// Check if it's a context cancellation
			if ctx.Err() != nil {
				return "", fmt.Errorf("context cancelled: %w", ctx.Err())
			}
			if i == maxRetries-1 {
				return "", fmt.Errorf("failed after %d retries: %w", maxRetries, err)
			}
			// Shorter delay between retries
			delay := baseDelay * time.Duration(math.Pow(2, float64(i)))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			}
			continue
		}

		// Check for non-200 status codes
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Read the body for more error info
			var errBody bytes.Buffer
			errBody.ReadFrom(resp.Body)
			resp.Body.Close()

			// Check for 5xx errors which are candidates for retry
			if resp.StatusCode >= 500 && i < maxRetries-1 {
				delay := baseDelay * time.Duration(math.Pow(2, float64(i)))
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
				}
				continue
			}
			return "", fmt.Errorf("API request failed with status %s: %s", resp.Status, errBody.String())
		}

		// If successful, break the loop
		break
	}
	defer resp.Body.Close()

	// 3. Parse the response
	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// 4. Extract the text
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

// --- A2A Endpoints ---

// agentCardHandler returns the agent card information
func agentCardHandler(c *gin.Context) {
	// Get the base URL from the request
	scheme := "http"
	if c.GetHeader("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
		scheme = "https"
	}
	host := c.GetHeader("Host")
	if host == "" {
		host = c.Request.Host
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, host)

	agentCard := AgentCard{
		Name:        "site_scout",
		Description: "A web analysis agent that provides website summaries and finds alternative websites",
		URL:         baseURL,
		Version:     "1.0.0",
		Author:      "Your Name",
		Categories:  []string{"utilities", "web"},
	}

	c.JSON(http.StatusOK, agentCard)
}

// jsonRPCHandler handles JSON-RPC requests
func jsonRPCHandler(c *gin.Context) {
	var rpcReq JSONRPCRequest

	if err := c.ShouldBindJSON(&rpcReq); err != nil {
		sendRPCError(c, nil, -32700, "Parse error", err.Error())
		return
	}

	// Validate JSON-RPC version
	if rpcReq.JSONRPC != "2.0" {
		sendRPCError(c, rpcReq.ID, -32600, "Invalid Request", "jsonrpc must be '2.0'")
		return
	}

	// Route based on method
	switch rpcReq.Method {
	case "message/send":
		handleMessageSend(c, &rpcReq)
	default:
		sendRPCError(c, rpcReq.ID, -32601, "Method not found", fmt.Sprintf("Method '%s' not found", rpcReq.Method))
	}
}

// handleMessageSend processes the message/send method
func handleMessageSend(c *gin.Context, rpcReq *JSONRPCRequest) {
	if rpcReq.Params == nil || rpcReq.Params.Message == nil {
		sendRPCError(c, rpcReq.ID, -32602, "Invalid params", "Missing message in params")
		return
	}

	message := rpcReq.Params.Message
	if len(message.Parts) == 0 {
		sendRPCError(c, rpcReq.ID, -32602, "Invalid params", "Message must have at least one part")
		return
	}

	// Get the text part at index 0 (system's interpretation)
	// Support both "kind" and "type" fields for backward compatibility
	var textPart *RPCPart

	// First, check index 0 for the system interpretation
	if len(message.Parts) > 0 {
		part0 := message.Parts[0]
		isTextPart := part0.Kind == "text" || part0.Type == "text"
		if isTextPart && part0.Text != "" {
			textPart = &part0
		}
	}

	// If no text part found at index 0, try to find any text part
	if textPart == nil {
		for i := range message.Parts {
			part := &message.Parts[i]
			isTextPart := part.Kind == "text" || part.Type == "text"
			if isTextPart && part.Text != "" {
				textPart = part
				break
			}
		}
	}

	if textPart == nil || textPart.Text == "" {
		sendRPCError(c, rpcReq.ID, -32602, "Invalid params", "Message must contain at least one text part with text")
		return
	}

	// Extract URL from the system interpretation text (index 0)
	userMessage := textPart.Text
	url := extractURL(userMessage)

	if url == "" {
		// If no URL found, optionally check conversation history from data part
		// Look for data part with conversation history
		for _, part := range message.Parts {
			if part.Kind == "data" && len(part.Data) > 0 {
				// Search through conversation history for URLs
				for _, item := range part.Data {
					if item.Kind == "text" && item.Text != "" {
						if foundURL := extractURL(item.Text); foundURL != "" {
							url = foundURL
							log.Printf("Found URL in conversation history: %s\n", url)
							break
						}
					}
				}
			}
		}
	}

	if url == "" {
		// If no URL found, return a helpful message
		responseText := "Please provide a URL to analyze. I can help you by:\n1. Summarizing the website content\n2. Finding alternative websites\n\nExample: Analyze https://example.com"
		sendRPCSuccess(c, rpcReq.ID, responseText)
		return
	}

	log.Printf("A2A request: Analyzing URL %s (from system interpretation: %s)\n", url, userMessage)

	// Run the agent logic
	summary, alternatives, err := runAgent(url)
	if err != nil {
		log.Printf("Agent failed for %s: %v\n", url, err)
		responseText := fmt.Sprintf("I encountered an error while analyzing %s: %s", url, err.Error())
		sendRPCSuccess(c, rpcReq.ID, responseText)
		return
	}

	// Format the response
	responseText := fmt.Sprintf("Analysis for %s:\n\n**Summary:**\n%s\n\n**Alternative Websites:**\n%s", url, summary, alternatives)
	sendRPCSuccess(c, rpcReq.ID, responseText)
}

// sendRPCSuccess sends a successful JSON-RPC response
func sendRPCSuccess(c *gin.Context, id interface{}, text string) {
	messageID := uuid.New().String()
	taskID := uuid.New().String()

	// Build message and task once
	msg := map[string]interface{}{
		"role":      "agent",
		"parts":     []RPCPart{{Kind: "text", Text: text}},
		"kind":      "message",
		"messageId": messageID,
	}
	task := map[string]interface{}{
		"id":     taskID,
		"status": "completed",
	}

	// Result with both variants and flattened message fields for compatibility
	result := map[string]interface{}{
		// Capitalized and lowercase variants
		"Task":    task,
		"Message": msg,
		// Flattened message fields at top-level (some validators expect this)
		"role":      "agent",
		"parts":     []RPCPart{{Kind: "text", Text: text}},
		"kind":      "message",
		"messageId": messageID,
	}

	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}

	c.JSON(http.StatusOK, response)
}

// sendRPCError sends an error JSON-RPC response
func sendRPCError(c *gin.Context, id interface{}, code int, message string, data string) {
	response := JSONRPCErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}

	// Log the error
	if data != "" {
		log.Printf("JSON-RPC Error [%d]: %s - %s\n", code, message, data)
	} else {
		log.Printf("JSON-RPC Error [%d]: %s\n", code, message)
	}

	c.JSON(http.StatusOK, response)
}

// extractURL extracts a URL from text using regex
func extractURL(text string) string {
	// URL regex pattern
	urlPattern := regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)
	matches := urlPattern.FindString(text)
	if matches != "" {
		// Validate it's a proper URL
		if strings.HasPrefix(matches, "http://") || strings.HasPrefix(matches, "https://") {
			// Basic validation: check if it contains at least a dot (domain)
			if strings.Contains(matches, ".") {
				return matches
			}
		}
	}
	return ""
}
