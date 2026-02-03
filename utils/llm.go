package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"rss-reader/globals"
	"rss-reader/models"
	"sort"
	"strings"
	"sync"
	"time"
)

// FilterResponse AI过滤响应结构
type FilterResponse struct {
	IsFiltered bool    `json:"is_filtered"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// LLMClient 大模型客户端
type LLMClient struct {
	config models.AIFilterConfig
	client *http.Client
}

// NewLLMClient 创建新的LLM客户端
func NewLLMClient(config models.AIFilterConfig) *LLMClient {
	return &LLMClient{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.GetTimeout()) * time.Second,
		},
	}
}

// ChatMessage 聊天消息结构
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 聊天请求结构
type ChatRequest struct {
	Model          string         `json:"model"`
	Messages       []ChatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat 响应格式
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatResponse 聊天响应结构
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ClassifyItem 对RSS文章进行分类
// keywordOnly: 如果为true，只进行关键词过滤，不调用AI
func (c *LLMClient) ClassifyItem(item models.Item, strategy *models.FilterStrategy, keywordOnly bool) (*FilterResponse, error) {
	// 先检查关键词过滤
	if strategy != nil {
		// 检查保留关键词
		for _, keyword := range strategy.KeepKeywords {
			if containsKeyword(item.Title, keyword) || containsKeyword(item.Description, keyword) {
				return &FilterResponse{
					IsFiltered: false,
					Confidence: 1.0,
					Reason:     fmt.Sprintf("包含保留关键词: %s", keyword),
				}, nil
			}
		}
		// 检查过滤关键词
		for _, keyword := range strategy.FilterKeywords {
			if containsKeyword(item.Title, keyword) || containsKeyword(item.Description, keyword) {
				return &FilterResponse{
					IsFiltered: true,
					Confidence: 1.0,
					Reason:     fmt.Sprintf("包含过滤关键词: %s", keyword),
				}, nil
			}
		}
	}

	// 如果只需要关键词过滤，不调用AI
	if keywordOnly {
		return &FilterResponse{
			IsFiltered: false,
			Confidence: 0,
			Reason:     "关键词过滤未命中",
		}, nil
	}

	// 构建文章内容
	content := buildItemContent(item)

	// 获取系统提示词
	systemPrompt := c.config.GetSystemPrompt()
	if strategy != nil && strategy.CustomPrompt != "" {
		systemPrompt = strategy.CustomPrompt
	}

	// 构建请求
	reqBody := ChatRequest{
		Model: c.config.GetModel(),
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: content},
		},
		Temperature: c.config.GetTemperature(),
		MaxTokens:   c.config.GetMaxTokens(),
		ResponseFormat: &ResponseFormat{
			Type: "json_object",
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 发送请求
	apiURL := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(c.config.GetAPIBase(), "/"))
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.APIKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API错误: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("API未返回有效响应")
	}

	// 解析过滤结果
	responseContent := chatResp.Choices[0].Message.Content
	return parseFilterResponse(responseContent)
}

// buildItemContent 构建文章内容用于分类
func buildItemContent(item models.Item) string {
	var content strings.Builder
	content.WriteString("标题: ")
	content.WriteString(item.Title)
	content.WriteString("\n")

	if item.Source != "" {
		content.WriteString("来源: ")
		content.WriteString(item.Source)
		content.WriteString("\n")
	}

	if item.Description != "" {
		// 移除HTML标签
		desc := stripHTML(item.Description)
		// 限制长度
		if len(desc) > 2000 {
			desc = desc[:2000] + "..."
		}
		content.WriteString("内容: ")
		content.WriteString(desc)
	}

	return content.String()
}

// stripHTML 移除HTML标签
func stripHTML(html string) string {
	// 移除HTML标签
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, " ")
	// 清理多余空白
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// containsKeyword 检查文本是否包含关键词（不区分大小写）
func containsKeyword(text, keyword string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}

// parseFilterResponse 解析过滤响应
func parseFilterResponse(content string) (*FilterResponse, error) {
	// 移除可能的代码块标记
	content = stripCodeFences(content)

	var resp FilterResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		// 尝试解析数组格式（某些模型可能返回数组）
		var respArray []FilterResponse
		if err2 := json.Unmarshal([]byte(content), &respArray); err2 == nil && len(respArray) > 0 {
			return &respArray[0], nil
		}
		return nil, fmt.Errorf("解析过滤响应失败: %w, 内容: %s", err, content)
	}

	return &resp, nil
}

// stripCodeFences 移除代码块标记
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	// 移除 ```json 和 ``` 标记
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// filterResult AI过滤结果
type filterResult struct {
	index      int
	item       models.Item
	isFiltered bool
	fromCache  bool
	err        error
	confidence float64
	reason     string
}

// FilterItems 过滤Feed中的Items（并行处理）
func FilterItems(items []models.Item, rssURL string) []models.Item {
	config := globals.RssUrls.AIFilter
	strategy := getFilterStrategy(rssURL)

	// 检查是否只使用关键词过滤（不使用AI）
	useAI := ShouldUseAI(rssURL)
	keywordOnly := !useAI

	client := NewLLMClient(config)
	threshold := config.GetThreshold()
	if strategy != nil {
		threshold = strategy.GetThreshold(threshold)
	}

	// 获取并发数
	concurrency := config.GetConcurrency()
	if concurrency > len(items) {
		concurrency = len(items)
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	// 创建工作通道和结果通道
	jobChan := make(chan struct {
		index int
		item  models.Item
	}, len(items))
	resultChan := make(chan filterResult, len(items))

	// 启动worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				result := filterResult{
					index: job.index,
					item:  job.item,
				}

				// 先检查缓存
				globals.FilterCacheLock.RLock()
				cacheEntry, cached := globals.FilterCache[job.item.Link]
				globals.FilterCacheLock.RUnlock()

				if cached {
					// 使用缓存结果
					result.isFiltered = cacheEntry.IsFiltered
					result.fromCache = true
					if result.isFiltered {
						log.Printf("[过滤命中-来自缓存] 文章 [%s]", job.item.Title)
					}
				} else {
					// 没有缓存，调用过滤逻辑（带重试机制）
					const maxRetries = 3
					const retryDelay = 3 * time.Second
					
					var resp *FilterResponse
					var lastErr error
					
					for attempt := 1; attempt <= maxRetries; attempt++ {
						resp, lastErr = client.ClassifyItem(job.item, strategy, keywordOnly)
						if lastErr == nil {
							break
						}
						
						if attempt < maxRetries {
							log.Printf("[过滤重试] 文章 [%s]: 第 %d 次尝试失败: %v，%d秒后重试...", 
								job.item.Title, attempt, lastErr, int(retryDelay.Seconds()))
							time.Sleep(retryDelay)
						}
					}
					
					if lastErr != nil {
						result.err = lastErr
						log.Printf("[过滤失败] 文章 [%s]: 已重试 %d 次，最终失败: %v", job.item.Title, maxRetries, lastErr)
						// 失败后不存入缓存，下次源更新时将重新处理
					} else {
						result.isFiltered = resp.IsFiltered && resp.Confidence >= threshold
						result.confidence = resp.Confidence
						result.reason = resp.Reason

						if result.isFiltered {
							log.Printf("[过滤命中-新处理] 文章 [%s]: %s (置信度: %.2f)", job.item.Title, resp.Reason, resp.Confidence)
						} else if resp.Confidence > 0 {
							// 即使没被过滤，如果是新处理的且有置信度，也可以选择性记录（可选）
							// log.Printf("[AI过滤通过-新处理] 文章 [%s]: %s (置信度: %.2f)", job.item.Title, resp.Reason, resp.Confidence)
						}

						// 成功后存入缓存
						globals.FilterCacheLock.Lock()
						globals.FilterCache[job.item.Link] = models.FilterCacheEntry{
							IsFiltered: result.isFiltered,
						}
						globals.FilterCacheLock.Unlock()
						
						// 标记数据已变更，需要持久化
						MarkDataChanged()
					}
				}

				resultChan <- result
			}
		}()
	}

	// 发送所有任务
	for i, item := range items {
		jobChan <- struct {
			index int
			item  models.Item
		}{index: i, item: item}
	}
	close(jobChan)

	// 等待所有worker完成
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果并按原始顺序排序
	results := make([]filterResult, 0, len(items))
	for result := range resultChan {
		results = append(results, result)
	}

	// 按索引排序保持原顺序
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	// 统计并构建最终结果
	filteredItems := make([]models.Item, 0, len(items))
	cacheHits := 0
	newItems := 0
	filteredByCache := 0
	filteredByAI := 0
	failedItems := 0

	for _, result := range results {
		if result.fromCache {
			cacheHits++
			if result.isFiltered {
				filteredByCache++
				continue
			}
		} else if result.err != nil {
			failedItems++
			// 过滤失败时保留文章
			filteredItems = append(filteredItems, result.item)
			continue
		} else {
			newItems++
			if result.isFiltered {
				filteredByAI++
				continue
			}
		}

		filteredItems = append(filteredItems, result.item)
	}

	// 只在有新判断时展示统计
	if newItems > 0 || failedItems > 0 {
		log.Printf("[过滤统计] 源 [%s]: 并发数 %d，新判断 %d 篇，过滤 %d 篇，保留 %d 篇 | 缓存命中 %d 篇（其中过滤 %d 篇）", 
			rssURL, concurrency, newItems, filteredByAI, newItems-filteredByAI, cacheHits, filteredByCache)
	}

	return filteredItems
}

// getFilterStrategy 获取指定URL的过滤策略
func getFilterStrategy(rssURL string) *models.FilterStrategy {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.Filter
		}
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL {
					if feedURL.Filter != nil {
						return feedURL.Filter
					}
					return source.Filter
				}
			}
		}
	}
	return nil
}

// ShouldFilter 检查是否应该启用过滤（关键词或AI）
func ShouldFilter(rssURL string) bool {
	config := globals.RssUrls.AIFilter

	// 获取该源的特定策略
	strategy := getFilterStrategy(rssURL)
	if strategy == nil {
		return false
	}

	// 检查是否启用关键词过滤
	if strategy.IsKeywordEnabled() {
		return true
	}

	// 检查是否启用AI过滤（需要全局AI过滤启用且有API Key）
	if config.Enabled && config.APIKey != "" && strategy.IsAIEnabled() {
		return true
	}

	return false
}

// ShouldUseAI 检查是否应该使用AI过滤
func ShouldUseAI(rssURL string) bool {
	config := globals.RssUrls.AIFilter
	if !config.Enabled || config.APIKey == "" {
		return false
	}

	strategy := getFilterStrategy(rssURL)
	if strategy == nil {
		return false
	}

	return strategy.IsAIEnabled()
}
