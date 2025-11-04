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

const geminiAPIEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-preview-09-2025:generateContent?key="

var apiKey string

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
}

type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

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

type APIAgentRequest struct {
	URL string `json:"url" binding:"required"`
}

type APIAgentResponse struct {
	URL          string `json:"url"`
	Summary      string `json:"summary"`
	Alternatives string `json:"alternatives"`
}

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	ID      interface{} `json:"id"`
	Params  *RPCParams  `json:"params"`
}

type JSONRPCSuccessResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  *RPCResult  `json:"result"`
}

type JSONRPCErrorResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Error   *RPCError   `json:"error"`
}

type RPCParams struct {
	Message *RPCMessage `json:"message"`
}

type RPCMessage struct {
	Role      string    `json:"role"`
	Parts     []RPCPart `json:"parts"`
	Kind      string    `json:"kind,omitempty"`
	MessageID string    `json:"messageId,omitempty"`
}

type RPCPart struct {
	Kind string         `json:"kind"`
	Type string         `json:"type,omitempty"`
	Text string         `json:"text,omitempty"`
	Data []DataPartItem `json:"data,omitempty"`
}

type DataPartItem struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type RPCResult struct {
	Task    RPCTask          `json:"Task"`
	Message RPCMessageResult `json:"Message"`
}

type RPCTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type RPCMessageResult struct {
	Role      string    `json:"role"`
	Parts     []RPCPart `json:"parts"`
	Kind      string    `json:"kind"`
	MessageID string    `json:"messageId"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type AgentCard struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Categories  []string `json:"categories"`
}

func main() {
	err := godotenv.Load(".env")
	if err != nil && gin.Mode() != gin.ReleaseMode {
		log.Fatalf("Error loading .env file: %v", err)
	}

	apiKey = os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY environment variable not set. Please set it before running.")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	router.GET("/.well-known/agent.json", agentCardHandler)
	router.POST("/", jsonRPCHandler)

	router.POST("/agent", agentHandler)

	port := "8080"
	log.Printf("Starting AI Agent API server on :%s\n", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func agentHandler(c *gin.Context) {
	var req APIAgentRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	log.Printf("Handling request for URL: %s\n", req.URL)

	summary, alternatives, err := runAgent(req.URL)
	if err != nil {
		log.Printf("Agent failed for %s: %v\n", req.URL, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process request: " + err.Error()})
		return
	}

	resp := APIAgentResponse{
		URL:          req.URL,
		Summary:      summary,
		Alternatives: alternatives,
	}
	c.JSON(http.StatusOK, resp)
}

func runAgent(url string) (string, string, error) {

	log.Println("Fetching and extracting content...")
	textContent, err := fetchAndExtract(url)
	if err != nil {
		return "", "", fmt.Errorf("could not fetch or extract content: %w", err)
	}
	if textContent == "" {
		return "", "", fmt.Errorf("no content extracted from %s", url)
	}
	log.Println("Content extracted successfully.")

	summaryText := textContent
	if len(summaryText) > 15000 {
		summaryText = string([]rune(summaryText)[:15000])
	}

	alternativesText := textContent
	if len(alternativesText) > 5000 {
		alternativesText = string([]rune(alternativesText)[:5000])
	}

	type APIResult struct {
		Result string
		Err    error
	}
	summaryChan := make(chan APIResult, 1)
	alternativesChan := make(chan APIResult, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	log.Println("Starting summary generation (concurrent)...")
	go func() {
		s, err := getSummary(ctx, summaryText)
		summaryChan <- APIResult{s, err}
	}()

	log.Println("Starting alternatives search (concurrent)...")
	go func() {

		a, err := getAlternatives(ctx, url, alternativesText)
		alternativesChan <- APIResult{a, err}
	}()

	select {
	case sumResult := <-summaryChan:
		if sumResult.Err != nil {
			return "", "", fmt.Errorf("could not generate summary: %w", sumResult.Err)
		}

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

	doc.Find("script, style").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	bodyText := doc.Find("body").Text()

	cleanedText := strings.Join(strings.Fields(bodyText), " ")

	return cleanedText, nil
}

func getSummary(ctx context.Context, text string) (string, error) {
	systemPrompt := "Provide a concise one-paragraph summary focusing on main purpose and key offerings."
	userPrompt := fmt.Sprintf("Summarize this website content:\n\n%s", text)

	return callGemini(ctx, systemPrompt, userPrompt, false)
}

func getAlternatives(ctx context.Context, url, textContent string) (string, error) {

	systemPrompt := "Find 3-5 alternative websites using Google Search. List each with URL and one-sentence description. Exclude the provided URL and its subdomains."
	userPrompt := fmt.Sprintf("Website: %s\nContent sample: %s\n\nFind similar alternatives.", url, textContent)

	return callGemini(ctx, systemPrompt, userPrompt, true)
}

func callGemini(ctx context.Context, systemPrompt, userPrompt string, useSearch bool) (string, error) {

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context cancelled: %w", ctx.Err())
	default:
	}

	fullURL := geminiAPIEndpoint + apiKey

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

	var resp *http.Response
	maxRetries := 3
	baseDelay := 500 * time.Millisecond

	client := &http.Client{
		Timeout: 25 * time.Second,
	}

	for i := 0; i < maxRetries; i++ {

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(payload))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err != nil {

			if ctx.Err() != nil {
				return "", fmt.Errorf("context cancelled: %w", ctx.Err())
			}
			if i == maxRetries-1 {
				return "", fmt.Errorf("failed after %d retries: %w", maxRetries, err)
			}

			delay := baseDelay * time.Duration(math.Pow(2, float64(i)))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {

			var errBody bytes.Buffer
			errBody.ReadFrom(resp.Body)
			resp.Body.Close()

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

		break
	}
	defer resp.Body.Close()

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

func agentCardHandler(c *gin.Context) {

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

func jsonRPCHandler(c *gin.Context) {
	var rpcReq JSONRPCRequest

	if err := c.ShouldBindJSON(&rpcReq); err != nil {
		sendRPCError(c, nil, -32700, "Parse error", err.Error())
		return
	}

	if rpcReq.JSONRPC != "2.0" {
		sendRPCError(c, rpcReq.ID, -32600, "Invalid Request", "jsonrpc must be '2.0'")
		return
	}

	switch rpcReq.Method {
	case "message/send":
		handleMessageSend(c, &rpcReq)
	default:
		sendRPCError(c, rpcReq.ID, -32601, "Method not found", fmt.Sprintf("Method '%s' not found", rpcReq.Method))
	}
}

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

	var textPart *RPCPart

	if len(message.Parts) > 0 {
		part0 := message.Parts[0]
		isTextPart := part0.Kind == "text" || part0.Type == "text"
		if isTextPart && part0.Text != "" {
			textPart = &part0
		}
	}

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

	userMessage := textPart.Text
	url := extractURL(userMessage)

	if url == "" {

		for _, part := range message.Parts {
			if part.Kind == "data" && len(part.Data) > 0 {

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

		responseText := "Please provide a URL to analyze. I can help you by:\n1. Summarizing the website content\n2. Finding alternative websites\n\nExample: Analyze https://example.com"
		sendRPCSuccess(c, rpcReq.ID, responseText)
		return
	}

	log.Printf("A2A request: Analyzing URL %s (from system interpretation: %s)\n", url, userMessage)

	summary, alternatives, err := runAgent(url)
	if err != nil {
		log.Printf("Agent failed for %s: %v\n", url, err)
		responseText := fmt.Sprintf("I encountered an error while analyzing %s: %s", url, err.Error())
		sendRPCSuccess(c, rpcReq.ID, responseText)
		return
	}

	responseText := fmt.Sprintf("Analysis for %s:\n\n**Summary:**\n%s\n\n**Alternative Websites:**\n%s", url, summary, alternatives)
	sendRPCSuccess(c, rpcReq.ID, responseText)
}

func sendRPCSuccess(c *gin.Context, id interface{}, text string) {
	messageID := uuid.New().String()
	taskID := uuid.New().String()

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

	result := map[string]interface{}{

		"Task":    task,
		"Message": msg,

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

func sendRPCError(c *gin.Context, id interface{}, code int, message string, data string) {
	response := JSONRPCErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}

	if data != "" {
		log.Printf("JSON-RPC Error [%d]: %s - %s\n", code, message, data)
	} else {
		log.Printf("JSON-RPC Error [%d]: %s\n", code, message)
	}

	c.JSON(http.StatusOK, response)
}

func extractURL(text string) string {

	urlPattern := regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)
	matches := urlPattern.FindString(text)
	if matches != "" {

		if strings.HasPrefix(matches, "http://") || strings.HasPrefix(matches, "https://") {

			if strings.Contains(matches, ".") {
				return matches
			}
		}
	}
	return ""
}
