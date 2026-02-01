package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"rss-reader/globals"
	"rss-reader/models"
	"sort"
	"strings"
	"sync"
	"time"
)

// PostProcessResponse AI后处理响应结构
type PostProcessResponse struct {
	Title   string `json:"title,omitempty"`
	Link    string `json:"link,omitempty"`
	PubDate string `json:"pubDate,omitempty"`
}

// postProcessResult 后处理结果
type postProcessResult struct {
	index     int
	item      models.Item
	fromCache bool
	err       error
}

// PostProcessItems 对Feed条目进行后处理（并行处理）
func PostProcessItems(items []models.Item, rssURL string) []models.Item {
	config := getPostProcessConfig(rssURL)
	if config == nil || !config.Enabled {
		return items
	}

	// 获取并发数（复用AI过滤的并发配置）
	concurrency := globals.RssUrls.AIFilter.GetConcurrency()
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
	resultChan := make(chan postProcessResult, len(items))

	// 启动worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				result := postProcessResult{
					index: job.index,
					item:  job.item,
				}
				
				// 获取原始链接作为缓存 key（优先使用 OriginalLink，如果没有则使用 Link）
				originalLink := job.item.OriginalLink
				if originalLink == "" {
					originalLink = job.item.Link
				}

				// 先检查缓存
				cacheEntry, cached := GetPostProcessCache(originalLink)
				if cached {
					// 使用缓存结果
					if config.ModifyTitle && cacheEntry.Title != "" {
						result.item.Title = cacheEntry.Title
					}
					if config.ModifyLink && cacheEntry.Link != "" {
						// 保存原始链接（如果还没有的话）
						if result.item.OriginalLink == "" {
							result.item.OriginalLink = result.item.Link
						}
						result.item.Link = cacheEntry.Link
					}
					if config.ModifyPubDate && cacheEntry.PubDate != "" {
						result.item.PubDate = cacheEntry.PubDate
					}
					result.fromCache = true
				} else {
					// 没有缓存，执行后处理
					var processedItem models.Item
					var err error

					if config.GetMode() == "script" {
						processedItem, err = processItemWithScript(job.item, config)
					} else {
						processedItem, err = processItemWithAI(job.item, config)
					}

					if err != nil {
						result.err = err
						log.Printf("后处理条目失败 [%s]: %v", job.item.Title, err)
					} else {
						// 如果后处理会修改 Link，先保存原始链接
						if config.ModifyLink && processedItem.Link != job.item.Link {
							processedItem.OriginalLink = job.item.Link
						}
						result.item = processedItem

						// 存入缓存（使用原始链接作为 key）
						entry := models.PostProcessCacheEntry{
							ProcessedAt: time.Now().Format("2006-01-02 15:04:05"),
						}
						if config.ModifyTitle {
							entry.Title = processedItem.Title
						}
						if config.ModifyLink {
							entry.Link = processedItem.Link
						}
						if config.ModifyPubDate {
							entry.PubDate = processedItem.PubDate
						}
						SetPostProcessCache(originalLink, entry)
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
	results := make([]postProcessResult, 0, len(items))
	for result := range resultChan {
		results = append(results, result)
	}

	// 按索引排序保持原顺序
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	// 构建最终结果
	processedItems := make([]models.Item, 0, len(items))
	cacheHits := 0
	newItems := 0
	failedItems := 0

	for _, result := range results {
		if result.fromCache {
			cacheHits++
		} else if result.err != nil {
			failedItems++
		} else {
			newItems++
		}
		processedItems = append(processedItems, result.item)
	}

	// 只在有新处理时展示统计
	if newItems > 0 || failedItems > 0 {
		log.Printf("后处理完成 [%s]: 并发数 %d，新处理 %d 篇，失败 %d 篇 | 缓存命中 %d 篇",
			rssURL, concurrency, newItems, failedItems, cacheHits)
	}

	return processedItems
}

// getPostProcessConfig 获取指定URL的后处理配置
func getPostProcessConfig(rssURL string) *models.PostProcessConfig {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.PostProcess
		}
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL {
					if feedURL.PostProcess != nil {
						return feedURL.PostProcess
					}
					return source.PostProcess
				}
			}
		}
	}
	return nil
}

// ShouldPostProcess 检查是否应该启用后处理
func ShouldPostProcess(rssURL string) bool {
	config := getPostProcessConfig(rssURL)
	return config != nil && config.Enabled
}

// processItemWithAI 使用AI处理条目
func processItemWithAI(item models.Item, config *models.PostProcessConfig) (models.Item, error) {
	aiConfig := globals.RssUrls.AIFilter
	if aiConfig.APIKey == "" {
		return item, fmt.Errorf("AI API Key未配置")
	}

	// 构建提示词
	prompt := config.Prompt
	if prompt == "" {
		prompt = "请对以下RSS条目进行处理，返回JSON格式：{\"title\": \"处理后的标题\", \"link\": \"处理后的链接\", \"pubDate\": \"处理后的发布时间\"}"
	}

	// 构建条目内容
	itemJSON, _ := json.Marshal(map[string]string{
		"title":   item.Title,
		"link":    item.Link,
		"pubDate": item.PubDate,
	})

	// 构建请求
	reqBody := ChatRequest{
		Model: aiConfig.GetModel(),
		Messages: []ChatMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: string(itemJSON)},
		},
		Temperature: aiConfig.GetTemperature(),
		MaxTokens:   aiConfig.GetMaxTokens(),
		ResponseFormat: &ResponseFormat{
			Type: "json_object",
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return item, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 发送请求
	client := &http.Client{
		Timeout: time.Duration(aiConfig.GetTimeout()) * time.Second,
	}
	apiURL := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(aiConfig.GetAPIBase(), "/"))
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return item, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", aiConfig.APIKey))

	resp, err := client.Do(req)
	if err != nil {
		return item, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return item, fmt.Errorf("读取响应失败: %w", err)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return item, fmt.Errorf("解析响应失败: %w", err)
	}

	if chatResp.Error != nil {
		return item, fmt.Errorf("API错误: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return item, fmt.Errorf("API未返回有效响应")
	}

	// 解析后处理结果
	responseContent := chatResp.Choices[0].Message.Content
	responseContent = stripCodeFences(responseContent)

	var postProcessResp PostProcessResponse
	if err := json.Unmarshal([]byte(responseContent), &postProcessResp); err != nil {
		return item, fmt.Errorf("解析后处理响应失败: %w, 内容: %s", err, responseContent)
	}

	// 应用处理结果
	processedItem := item
	if config.ModifyTitle && postProcessResp.Title != "" {
		processedItem.Title = postProcessResp.Title
	}
	if config.ModifyLink && postProcessResp.Link != "" {
		processedItem.Link = postProcessResp.Link
	}
	if config.ModifyPubDate && postProcessResp.PubDate != "" {
		processedItem.PubDate = postProcessResp.PubDate
	}

	return processedItem, nil
}

// processItemWithScript 使用脚本处理条目
func processItemWithScript(item models.Item, config *models.PostProcessConfig) (models.Item, error) {
	// 将条目转换为JSON
	itemJSON, err := json.Marshal(map[string]string{
		"title":       item.Title,
		"link":        item.Link,
		"pubDate":     item.PubDate,
		"source":      item.Source,
		"description": item.Description,
	})
	if err != nil {
		return item, fmt.Errorf("序列化条目失败: %w", err)
	}

	var cmd *exec.Cmd

	// 优先使用内联脚本内容
	if config.ScriptContent != "" {
		// 使用 bash -c 直接执行脚本内容
		cmd = exec.Command("bash", "-c", config.ScriptContent)
	} else if config.ScriptPath != "" {
		// 使用脚本文件
		cmd = exec.Command(config.ScriptPath)
	} else {
		return item, fmt.Errorf("脚本内容或脚本路径未配置")
	}

	cmd.Stdin = bytes.NewReader(itemJSON)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return item, fmt.Errorf("脚本执行失败: %s, stderr: %s", err, string(exitErr.Stderr))
		}
		return item, fmt.Errorf("脚本执行失败: %w", err)
	}

	// 解析脚本输出
	var postProcessResp PostProcessResponse
	if err := json.Unmarshal(output, &postProcessResp); err != nil {
		return item, fmt.Errorf("解析脚本输出失败: %w, 输出: %s", err, string(output))
	}

	// 应用处理结果
	processedItem := item
	if config.ModifyTitle && postProcessResp.Title != "" {
		processedItem.Title = postProcessResp.Title
	}
	if config.ModifyLink && postProcessResp.Link != "" {
		processedItem.Link = postProcessResp.Link
	}
	if config.ModifyPubDate && postProcessResp.PubDate != "" {
		processedItem.PubDate = postProcessResp.PubDate
	}

	return processedItem, nil
}
