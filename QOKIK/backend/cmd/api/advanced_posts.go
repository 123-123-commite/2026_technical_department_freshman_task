package main

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type recommendationCandidate struct {
	post     gin.H
	authorID int64
	approved time.Time
	name     string
	category string
	location string
	event    string
}

var asciiWords = regexp.MustCompile(`[a-z0-9]+`)
var datePattern = regexp.MustCompile(`(\d{4})\s*[-/.年]\s*(\d{1,2})\s*[-/.月]\s*(\d{1,2})\s*日?`)
var monthDayPattern = regexp.MustCompile(`(\d{1,2})\s*月\s*(\d{1,2})\s*日`)

func normalizeMatchText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 0x3400 && r <= 0x9fff) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nameTokens(s string) map[string]bool {
	normalized := normalizeMatchText(s)
	tokens := make(map[string]bool)
	for _, word := range asciiWords.FindAllString(normalized, -1) {
		tokens[word] = true
	}
	// Chinese names have no spaces. Use adjacent-character tokens so that
	// "蓝色水壶" and "水壶" share a meaningful token without an NLP dependency.
	runes := []rune(normalized)
	for i := 0; i+1 < len(runes); i++ {
		if runes[i] >= 0x3400 && runes[i] <= 0x9fff && runes[i+1] >= 0x3400 && runes[i+1] <= 0x9fff {
			tokens[string(runes[i:i+2])] = true
		}
	}
	return tokens
}

func parseEventDate(value string, fallbackYear int) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if match := datePattern.FindStringSubmatch(value); len(match) == 4 {
		y, _ := time.Parse("2006-1-2", match[1]+"-"+match[2]+"-"+match[3])
		if !y.IsZero() {
			return y, true
		}
	}
	if match := monthDayPattern.FindStringSubmatch(value); len(match) == 3 {
		m, _ := time.Parse("2006-1-2", strconv.Itoa(fallbackYear)+"-"+match[1]+"-"+match[2])
		if !m.IsZero() {
			return m, true
		}
	}
	return time.Time{}, false
}

func recommendationMatch(source, candidate recommendationCandidate) []string {
	reasons := []string{}
	shared := sharedNameTokenCount(source.name, candidate.name)
	if shared > 0 {
		reasons = append(reasons, "名称关键词相近")
	}
	if a, b := normalizeMatchText(source.category), normalizeMatchText(candidate.category); a != "" && b != "" && (a == b || strings.Contains(a, b) || strings.Contains(b, a)) {
		reasons = append(reasons, "类别相同")
	}
	if a, b := normalizeMatchText(source.location), normalizeMatchText(candidate.location); a != "" && b != "" && (a == b || strings.Contains(a, b) || strings.Contains(b, a)) {
		reasons = append(reasons, "地点相近")
	}
	if a, okA := parseEventDate(source.event, source.approved.Year()); okA {
		if b, okB := parseEventDate(candidate.event, candidate.approved.Year()); okB && math.Abs(a.Sub(b).Hours()) <= 24*30 {
			reasons = append(reasons, "时间接近")
		}
	}
	return reasons
}

func recommendationStrength(source, candidate recommendationCandidate, reasons []string) int {
	// Each matched field contributes one point; repeated name keywords add
	// strength so a closer item-name match ranks ahead of a single token match.
	return len(reasons) + sharedNameTokenCount(source.name, candidate.name)
}

func sharedNameTokenCount(leftName, rightName string) int {
	left, right := nameTokens(leftName), nameTokens(rightName)
	shared := 0
	for token := range left {
		if right[token] {
			shared++
		}
	}
	return shared
}

func (s *server) recommendations(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 404, "内容不存在")
		return
	}
	var source recommendationCandidate
	var sourceKind, sourceLifecycle, sourceModeration string
	var sourceWithdrawn bool
	err = s.db.QueryRow(c, `SELECT id,author_id,kind,item_name,COALESCE(category,''),COALESCE(location,''),COALESCE(event_time,''),COALESCE(approved_at,created_at),lifecycle_status,moderation_status,withdrawn FROM posts WHERE id=$1`, id).Scan(&id, &source.authorID, &sourceKind, &source.name, &source.category, &source.location, &source.event, &source.approved, &sourceLifecycle, &sourceModeration, &sourceWithdrawn)
	if err != nil || sourceModeration != "已通过" || sourceWithdrawn || (sourceKind != "lost" && sourceKind != "found") {
		fail(c, 404, "内容不存在")
		return
	}
	if sourceLifecycle != activeLifecycle(sourceKind) {
		c.JSON(200, gin.H{"items": []gin.H{}})
		return
	}
	targetKind := "found"
	if sourceKind == "found" {
		targetKind = "lost"
	}
	rows, err := s.db.Query(c, `SELECT id,author_id,kind,item_name,description,category,location,event_time,lifecycle_status,moderation_status,created_at,approved_at FROM posts WHERE kind=$1 AND moderation_status='已通过' AND withdrawn=false AND lifecycle_status=$2 AND author_id<>$3 AND id<>$4 AND COALESCE(GREATEST(freshness_confirmed_at,approved_at),approved_at,created_at)>now()-interval '30 days' ORDER BY COALESCE(approved_at,created_at) DESC,id DESC`, targetKind, activeLifecycle(targetKind), source.authorID, id)
	if err != nil {
		fail(c, 500, "推荐查询失败")
		return
	}
	defer rows.Close()
	candidates := []recommendationCandidate{}
	for rows.Next() {
		var candidate recommendationCandidate
		var postID int64
		var description, lifecycle, moderation string
		var created time.Time
		var approved *time.Time
		var category, location, event *string
		var kind string
		if err = rows.Scan(&postID, &candidate.authorID, &kind, &candidate.name, &description, &category, &location, &event, &lifecycle, &moderation, &created, &approved); err != nil {
			fail(c, 500, "推荐查询失败")
			return
		}
		candidate.category, candidate.location, candidate.event = ptrString(category), ptrString(location), ptrString(event)
		candidate.approved = created
		if approved != nil {
			candidate.approved = *approved
		}
		candidate.post = gin.H{"id": postID, "kind": kind, "item_name": candidate.name, "description": description, "category": category, "location": location, "event_time": event, "lifecycle_status": lifecycle, "moderation_status": moderation, "created_at": created, "approved_at": approved}
		candidates = append(candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		fail(c, 500, "推荐查询失败")
		return
	}
	type ranked struct {
		item  gin.H
		score int
		date  time.Time
		id    int64
	}
	results := []ranked{}
	for _, candidate := range candidates {
		reasons := recommendationMatch(source, candidate)
		if len(reasons) == 0 {
			continue
		}
		candidate.post["match_reasons"] = reasons
		idValue := candidate.post["id"].(int64)
		results = append(results, ranked{item: candidate.post, score: recommendationStrength(source, candidate, reasons), date: candidate.approved, id: idValue})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if !results[i].date.Equal(results[j].date) {
			return results[i].date.After(results[j].date)
		}
		return results[i].id > results[j].id
	})
	items := []gin.H{}
	for i := 0; i < len(results) && i < 5; i++ {
		items = append(items, results[i].item)
	}
	c.JSON(200, gin.H{"items": items})
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func activeLifecycle(kind string) string {
	if kind == "found" {
		return "待认领"
	}
	return "寻找中"
}

func (s *server) confirmPost(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 404, "内容不存在")
		return
	}
	var authorID int64
	if err = s.db.QueryRow(c, "SELECT author_id FROM posts WHERE id=$1", id).Scan(&authorID); err != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if authorID != current(c).ID {
		fail(c, 403, "只能确认自己的内容")
		return
	}
	result, err := s.db.Exec(c, `UPDATE posts SET freshness_confirmed_at=now() WHERE id=$1 AND moderation_status='已通过' AND withdrawn=false AND lifecycle_status IN ('寻找中','待认领')`, id)
	if err != nil {
		fail(c, 500, "确认失败")
		return
	}
	if result.RowsAffected() == 0 {
		fail(c, 409, "当前内容无需确认或无权操作")
		return
	}
	p, err := s.postByID(c, id, false)
	if err != nil {
		fail(c, 500, "确认失败")
		return
	}
	c.JSON(200, p)
}
