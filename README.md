# Go AI Agent API (Gin Version): Website Summarizer & Alternative Finder

This Go-based AI Agent API enables users to quickly obtain a concise summary and a curated list of alternative websites for any given URL. Built using the Gin web framework, the agent fetches web content, intelligently extracts and condenses the main information using the Gemini API, and leverages Google Search to suggest similar sites—all through a single, easy-to-use REST endpoint. It is designed to be efficient, concurrent, and ideal for anyone needing fast insights or recommendations on web resources.


This is a Go REST API server built with the Gin framework. It uses the Gemini API to:

- Accept a POST request with a URL.
- Fetch and parse the content of that URL.
- Generate a concise summary of the content.
- Find alternative/similar websites using Google Search.
- Return the summary and alternatives as a JSON response.

---

## Prerequisites

- **Go:** You must have the Go programming language installed (version 1.20 or later).
- **Gemini API Key:** You need an API key from [Google AI Studio](https://aistudio.google.com/).

  1. Go to Google AI Studio.
  2. Click **"Get API key"** and follow the instructions.

---

## Setup & Running

### 1. Save the Code

- Save the Go code above into a file named `main.go`.
- Save this text into a file named `README.md`.

### 2. Create a Go Module

Open your terminal in the directory where you saved `main.go`:

```sh
go mod init ai-agent-api-gin
```

### 3. Install Dependencies

This tool uses `goquery` to parse HTML and `gin` for the web server. Install them:

```sh
go get github.com/PuerkitoBio/goquery
go get github.com/gin-gonic/gin
```

The Go toolchain will automatically add them to your `go.mod` and `go.sum` files.

### 4. Set Your API Key

You must set your Gemini API key as an environment variable.

<details>
<summary>macOS / Linux</summary>

```sh
export GEMINI_API_KEY="YOUR_API_KEY_HERE"
```

</details>

<details>
<summary>Windows (Command Prompt)</summary>

```cmd
set GEMINI_API_KEY="YOUR_API_KEY_HERE"
```

</details>

<details>
<summary>Windows (PowerShell)</summary>

```powershell
$env:GEMINI_API_KEY="YOUR_API_KEY_HERE"
```

</details>

### 5. Run the Server

Now you can run the server:

```sh
go run main.go
```

You will see Gin's startup messages, and a line similar to:

```
Starting AI Agent API server on :8080
```

---

## Using the API

The API has one endpoint: `POST /agent`.

You need to send a JSON payload containing the url you want to analyze.

### Example Request (using cURL)

Open a new terminal window and run the following command. The example uses https://gin-gonic.com, a site that works reliably.

```sh
curl -X POST http://localhost:8080/agent \
  -H "Content-Type: application/json" \
  -d '{"url": "https://gin-gonic.com"}'
```

### Example Success Response

You will receive a JSON response similar to:

```json
{
  "url":"https://gin-gonic.com",
  "summary":"The Gin Web Framework is presented as the fastest full-featured web framework for Go (Golang), offering a high-performance, productivity-focused alternative to frameworks like Martini by utilizing Radix tree-based routing and avoiding reflection for speeds up to 40 times faster. Its core offerings include robust middleware support for handling request chains, crash recovery capabilities to maintain server uptime, convenient JSON validation, organized routing through infinitely nestable groups, and comprehensive error management. Gin also provides built-in APIs for easy rendering of JSON, XML, and HTML content, making it highly versatile and extendable.",
  "alternatives":"Based on the summary of the Gin web framework, here are four alternative Go (Golang) web frameworks, focusing on similar areas of performance, productivity, and features:\n\n| Framework Name | URL | Description |\n| :--- | :--- | :--- |\n| **Echo** | https://echo.labstack.com | Echo is a high-performance, minimalist, and extensible web framework for building robust and scalable applications in Go, featuring a highly optimized router and automatic TLS. |\n| **Fiber** | https://gofiber.io | Fiber is an Express.js-inspired web framework built on top of the extremely fast Fasthttp engine, designed for rapid development with a focus on performance and minimal memory allocation. |\n| **Beego** | https://beego.me | Beego is a full-featured, high-performance Go web framework, similar to Django or Flask, that uses an MVC architecture and includes built-in tools like ORM, caching, and session handling for enterprise-grade applications. |\n| **Revel** | https://revel.dev | Revel is a high-productivity, full-stack Go web framework that handles all the needs of a web application and features a unique \"hot-reload\" capability that instantly rebuilds and redeploys the code on file change. |"
}
```

### Example Error Response

If you send an invalid request (e.g., an empty request body):

```sh
curl -X POST http://localhost:8080/agent -d '{}'
```

You will get an error:

```json
{
  "error": "Invalid request: Key: 'APIAgentRequest.URL' Error:Field validation for 'URL' failed on the 'required' tag"
}
```

