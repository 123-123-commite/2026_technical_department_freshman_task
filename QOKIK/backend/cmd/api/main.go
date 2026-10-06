package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
	"hduhelp/backend/migrations"
)

type server struct {
	db               *pgxpool.Pool
	secret           []byte
	issuer, audience string
	cookieSecure     bool
}
type claims struct {
	Use    string `json:"use"`
	Family string `json:"family,omitempty"`
	jwt.RegisteredClaims
}
type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	Student  string `json:"student_number,omitempty"`
}
type postInput struct {
	Kind          string  `json:"kind"`
	Item          string  `json:"item_name"`
	Description   string  `json:"description"`
	Category      *string `json:"category"`
	Location      *string `json:"location"`
	EventTime     *string `json:"event_time"`
	PrivateDetail *string `json:"private_verification_detail"`
}

func main() {
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		log.Fatal("JWT_SECRET must contain at least 32 characters")
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	if err = migrate(ctx, db); err != nil {
		log.Fatal(err)
	}
	s := &server{db: db, secret: []byte(secret), issuer: env("JWT_ISSUER", "hduhelp-local"), audience: env("JWT_AUDIENCE", "hduhelp-web"), cookieSecure: strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true")}
	if err = s.bootstrapAdmin(ctx); err != nil {
		log.Fatal(err)
	}
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	})
	s.routes(r)
	log.Fatal(r.Run("0.0.0.0:8000"))
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func allowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range strings.Split(env("ALLOWED_ORIGINS", "http://127.0.0.1:5173,http://localhost:5173,http://127.0.0.1:5174"), ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}
func migrate(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		return err
	}
	files, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		var exists bool
		if err = db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, e := migrations.Files.ReadFile(name)
		if e != nil {
			return e
		}
		tx, e := db.Begin(ctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, string(sql)); e == nil {
			_, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", name)
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, e)
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
	}
	return nil
}
func (s *server) bootstrapAdmin(ctx context.Context) error {
	name, password := os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD")
	if name == "" || password == "" {
		return errors.New("ADMIN_USERNAME and ADMIN_PASSWORD are required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	err = tx.QueryRow(ctx, "SELECT role FROM users WHERE username=$1 FOR UPDATE", name).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, "INSERT INTO users(username,password_hash,nickname,student_number,role) VALUES($1,$2,'系统管理员',$3,'admin')", name, hashPassword(password), "admin:"+name)
	} else if err == nil && role != "admin" {
		return errors.New("管理员用户名已被普通账号占用；请改用独立用户名")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func hashPassword(p string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	hash := argon2.IDKey([]byte(p), salt, 2, 19*1024, 1, 32)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash)
}
func verifyPassword(p, encoded string) bool {
	parts := strings.Split(encoded, ":")
	if len(parts) != 2 {
		return false
	}
	salt, e1 := hex.DecodeString(parts[0])
	expected, e2 := hex.DecodeString(parts[1])
	if e1 != nil || e2 != nil {
		return false
	}
	actual := argon2.IDKey([]byte(p), salt, 2, 19*1024, 1, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func (s *server) sign(u user, use string, family string, lifetime time.Duration) (string, string, error) {
	id := uuid.NewString()
	now := time.Now()
	c := claims{Use: use, Family: family, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.issuer, Subject: strconv.FormatInt(u.ID, 10), Audience: jwt.ClaimStrings{s.audience}, ID: id, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(lifetime))}}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	v, e := t.SignedString(s.secret)
	return v, id, e
}
func (s *server) parse(token, use string) (*claims, error) {
	c := &claims{}
	t, e := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("invalid algorithm")
		}
		return s.secret, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithAudience(s.audience), jwt.WithExpirationRequired())
	if e != nil || !t.Valid || c.Use != use {
		return nil, errors.New("invalid token")
	}
	return c, nil
}
func (s *server) authenticate(c *gin.Context) bool {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(h, "Bearer ") {
		fail(c, 401, "请先登录")
		return false
	}
	cl, e := s.parse(strings.TrimPrefix(h, "Bearer "), "access")
	if e != nil {
		fail(c, 401, "登录已失效")
		return false
	}
	id, e := strconv.ParseInt(cl.Subject, 10, 64)
	if e != nil {
		fail(c, 401, "登录已失效")
		return false
	}
	var u user
	e = s.db.QueryRow(c, "SELECT id,username,nickname,role,student_number FROM users WHERE id=$1", id).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.Student)
	if e != nil {
		fail(c, 401, "登录已失效")
		return false
	}
	c.Set("user", u)
	return true
}
func (s *server) access(c *gin.Context) {
	if s.authenticate(c) {
		c.Next()
	}
}
func (s *server) admin(c *gin.Context) {
	if !s.authenticate(c) {
		return
	}
	u := current(c)
	if u.Role != "admin" {
		fail(c, 403, "需要管理员权限")
		return
	}
	c.Next()
}
func current(c *gin.Context) user { v, _ := c.Get("user"); return v.(user) }
func fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"detail": message})
}
func bind(c *gin.Context, v any) bool {
	if e := c.ShouldBindJSON(v); e != nil {
		fail(c, 422, "请求内容无效")
		return false
	}
	return true
}
func validText(v string, min, max int) bool {
	n := len(strings.TrimSpace(v))
	return n >= min && n <= max
}

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (s *server) setRefreshCookie(c *gin.Context, token string) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("hduhelp_refresh", token, 7*86400, "/api/auth", "", s.cookieSecure, true)
}
func (s *server) clearCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("hduhelp_refresh", "", -1, "/api/auth", "", s.cookieSecure, true)
}
func (s *server) issuePair(c *gin.Context, u user, family string, status int) error {
	access, _, e := s.sign(u, "access", "", 15*time.Minute)
	if e != nil {
		return e
	}
	if family == "" {
		family = uuid.NewString()
	}
	refresh, jti, e := s.sign(u, "refresh", family, 7*24*time.Hour)
	if e != nil {
		return e
	}
	cl, _ := s.parse(refresh, "refresh")
	_, e = s.db.Exec(c, `INSERT INTO refresh_tokens(jti,family_id,user_id,expires_at)
		SELECT $1,$2,$3,$4 WHERE NOT EXISTS (
			SELECT 1 FROM refresh_tokens WHERE family_id=$2 AND revoked_at IS NOT NULL
		)`, jti, family, u.ID, cl.ExpiresAt.Time)
	if e != nil {
		return e
	}
	var inserted bool
	if e = s.db.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM refresh_tokens WHERE jti=$1)", jti).Scan(&inserted); e != nil || !inserted {
		if e != nil {
			return e
		}
		return errors.New("refresh token family was revoked")
	}
	s.setRefreshCookie(c, refresh)
	c.JSON(status, gin.H{"access_token": access, "token_type": "Bearer", "expires_in": 900, "user": profile(u)})
	return nil
}
func profile(u user) gin.H {
	return gin.H{"id": u.ID, "username": u.Username, "nickname": u.Nickname, "role": u.Role, "student_number": u.Student, "student_number_verified": false}
}
func (s *server) routes(r *gin.Engine) {
	r.GET("/api/health", func(c *gin.Context) {
		if e := s.db.Ping(c); e != nil {
			fail(c, 503, "database unavailable")
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.POST("/api/auth/register", s.register)
	r.POST("/api/auth/login", s.login)
	r.POST("/api/auth/refresh", s.refresh)
	r.POST("/api/auth/logout", s.logout)
	r.GET("/api/auth/me", s.access, func(c *gin.Context) { c.JSON(200, profile(current(c))) })
	r.GET("/api/posts", s.listPosts)
	r.GET("/api/posts/:id", s.publicPost)
	r.GET("/api/posts/:id/recommendations", s.recommendations)
	r.POST("/api/posts", s.access, s.createPost)
	r.GET("/api/my/posts", s.access, s.myPosts)
	r.POST("/api/posts/:id/confirm", s.access, s.confirmPost)
	r.PUT("/api/posts/:id", s.access, s.updatePost)
	r.POST("/api/posts/:id/status", s.access, s.setPostStatus)
	r.DELETE("/api/posts/:id", s.access, s.deletePost)
	r.POST("/api/posts/:id/requests", s.access, s.createRequest)
	r.GET("/api/my/requests", s.access, s.myRequests)
	r.GET("/api/posts/:id/requests", s.access, s.postRequests)
	r.GET("/api/requests/:id", s.access, s.requestDetail)
	r.POST("/api/requests/:id/withdraw", s.access, s.withdrawRequest)
	r.POST("/api/requests/:id/accept", s.access, s.acceptRequest)
	r.POST("/api/requests/:id/reject", s.access, s.rejectRequest)
	r.POST("/api/posts/:id/reports", s.access, s.reportPost)
	r.POST("/api/requests/:id/reports", s.access, s.reportRequest)
	r.GET("/api/admin/reviews", s.admin, s.pendingReviews)
	r.POST("/api/admin/reviews/:id/approve", s.admin, s.approvePost)
	r.POST("/api/admin/reviews/:id/reject", s.admin, s.rejectPost)
	r.POST("/api/admin/posts/:id/take-down", s.admin, s.takeDown)
	r.GET("/api/admin/reports", s.admin, s.adminReports)
	r.POST("/api/admin/reports/:id/dismiss", s.admin, s.dismissReport)
	r.POST("/api/admin/reports/:id/remove", s.admin, s.removeReported)
}
func (s *server) register(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Nickname string `json:"nickname"`
		Student  string `json:"student_number"`
	}
	if !bind(c, &in) {
		return
	}
	if len(in.Username) < 2 || len(in.Username) > 32 || !usernameRE.MatchString(in.Username) || len(in.Password) < 10 || len(in.Password) > 128 || !validText(in.Nickname, 1, 40) || len(in.Student) < 4 || len(in.Student) > 32 {
		fail(c, 422, "注册信息格式无效")
		return
	}
	var u user
	e := s.db.QueryRow(c, "INSERT INTO users(username,password_hash,nickname,student_number) VALUES($1,$2,$3,$4) RETURNING id,username,nickname,role,student_number", in.Username, hashPassword(in.Password), strings.TrimSpace(in.Nickname), in.Student).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.Student)
	if e != nil {
		fail(c, 409, "用户名或学号已注册")
		return
	}
	c.Status(201)
	if e = s.issuePair(c, u, "", http.StatusCreated); e != nil {
		fail(c, 500, "无法创建会话")
	}
}
func (s *server) login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &in) {
		return
	}
	var u user
	var hash string
	e := s.db.QueryRow(c, "SELECT id,username,nickname,role,student_number,password_hash FROM users WHERE username=$1", in.Username).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.Student, &hash)
	if e != nil || !verifyPassword(in.Password, hash) {
		fail(c, 401, "用户名或密码错误")
		return
	}
	if e = s.issuePair(c, u, "", http.StatusOK); e != nil {
		fail(c, 500, "无法创建会话")
	}
}
func (s *server) refresh(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if !allowedOrigin(origin) {
		fail(c, 403, "请求来源无效")
		return
	}
	raw, e := c.Cookie("hduhelp_refresh")
	if e != nil {
		fail(c, 401, "登录已失效")
		return
	}
	cl, e := s.parse(raw, "refresh")
	if e != nil {
		fail(c, 401, "登录已失效")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "刷新失败")
		return
	}
	defer tx.Rollback(c)
	var consumed, revoked *time.Time
	var uid int64
	var family string
	e = tx.QueryRow(c, "SELECT user_id,family_id,consumed_at,revoked_at FROM refresh_tokens WHERE jti=$1 FOR UPDATE", cl.ID).Scan(&uid, &family, &consumed, &revoked)
	if e != nil || revoked != nil {
		fail(c, 401, "登录已失效")
		return
	}
	if consumed != nil {
		if _, e = tx.Exec(c, "UPDATE refresh_tokens SET revoked_at=now() WHERE family_id=$1 AND revoked_at IS NULL", family); e != nil {
			fail(c, 500, "刷新失败")
			return
		}
		if e = tx.Commit(c); e != nil {
			fail(c, 500, "刷新失败")
			return
		}
		fail(c, 401, "检测到重复使用，当前设备已退出")
		return
	}
	_, e = tx.Exec(c, "UPDATE refresh_tokens SET consumed_at=now() WHERE jti=$1", cl.ID)
	if e != nil {
		fail(c, 500, "刷新失败")
		return
	}
	var u user
	e = tx.QueryRow(c, "SELECT id,username,nickname,role,student_number FROM users WHERE id=$1", uid).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.Student)
	if e != nil {
		fail(c, 401, "账号不存在")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "刷新失败")
		return
	}
	if e = s.issuePair(c, u, family, http.StatusOK); e != nil {
		fail(c, 500, "刷新失败")
	}
}
func (s *server) logout(c *gin.Context) {
	raw, _ := c.Cookie("hduhelp_refresh")
	if cl, e := s.parse(raw, "refresh"); e == nil {
		_, _ = s.db.Exec(c, "UPDATE refresh_tokens SET revoked_at=now() WHERE family_id=$1 AND revoked_at IS NULL", cl.Family)
	}
	s.clearCookie(c)
	c.JSON(200, gin.H{"ok": true})
}

func (s *server) postByID(c *gin.Context, id int64, public bool) (gin.H, error) {
	var p gin.H = gin.H{}
	var approved *time.Time
	var rejection *string
	var category, location, event *string
	var withdrawn bool
	var authorID int64
	var postID int64
	var kind, item, description, lifecycle, moderation, nickname string
	var privateDetail *string
	var freshnessConfirmed *time.Time
	var freshnessDue bool
	var created time.Time
	q := `SELECT p.id,p.author_id,p.kind,p.item_name,p.description,p.category,p.location,p.event_time,p.lifecycle_status,p.moderation_status,p.rejection_reason,p.withdrawn,p.created_at,p.approved_at,u.nickname,p.private_verification_detail,p.freshness_confirmed_at,(p.moderation_status='已通过' AND NOT p.withdrawn AND p.lifecycle_status IN ('寻找中','待认领') AND COALESCE(GREATEST(p.freshness_confirmed_at,p.approved_at),p.approved_at,p.created_at)<=now()-interval '30 days') FROM posts p JOIN users u ON u.id=p.author_id WHERE p.id=$1`
	e := s.db.QueryRow(c, q, id).Scan(&postID, &authorID, &kind, &item, &description, &category, &location, &event, &lifecycle, &moderation, &rejection, &withdrawn, &created, &approved, &nickname, &privateDetail, &freshnessConfirmed, &freshnessDue)
	if e != nil {
		return nil, e
	}
	if public && (moderation != "已通过" || withdrawn) {
		return nil, pgx.ErrNoRows
	}
	p["id"], p["kind"], p["item_name"], p["description"] = postID, kind, item, description
	p["lifecycle_status"], p["moderation_status"] = lifecycle, moderation
	p["created_at"], p["author_nickname"] = created, nickname
	p["category"] = category
	p["location"] = location
	p["event_time"] = event
	p["rejection_reason"] = rejection
	p["withdrawn"] = withdrawn
	p["approved_at"] = approved
	p["author_id"] = authorID
	p["freshness_status"] = "有效"
	if freshnessDue {
		p["freshness_status"] = "待确认"
	}
	p["requires_claim_verification"] = kind == "found" && privateDetail != nil && strings.TrimSpace(*privateDetail) != ""
	if !public {
		if value, ok := c.Get("user"); ok && value.(user).ID == authorID {
			p["private_verification_detail"] = privateDetail
		}
	}
	p["freshness_confirmed_at"] = freshnessConfirmed
	return p, nil
}
func (s *server) listPosts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := []string{"moderation_status='已通过'", "withdrawn=false"}
	args := []any{}
	add := func(key, value string) {
		if value != "" {
			args = append(args, value)
			where = append(where, fmt.Sprintf("%s=$%d", key, len(args)))
		}
	}
	add("kind", c.Query("kind"))
	add("lifecycle_status", c.Query("status"))
	if v := c.Query("location"); v != "" {
		args = append(args, "%"+v+"%")
		where = append(where, fmt.Sprintf("location ILIKE $%d", len(args)))
	}
	if v := c.Query("q"); v != "" {
		args = append(args, "%"+v+"%")
		where = append(where, fmt.Sprintf("(item_name ILIKE $%d OR description ILIKE $%d)", len(args), len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if e := s.db.QueryRow(c, "SELECT count(*) FROM posts WHERE "+clause, args...).Scan(&total); e != nil {
		fail(c, 500, "查询失败")
		return
	}
	args = append(args, limit, offset)
	rows, e := s.db.Query(c, "SELECT id FROM posts WHERE "+clause+fmt.Sprintf(" ORDER BY COALESCE(approved_at,created_at) DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		p, err := s.postByID(c, id, true)
		if err == nil {
			delete(p, "author_id")
			delete(p, "author_student_number")
			items = append(items, p)
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}
func (s *server) publicPost(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	p, e := s.postByID(c, id, true)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	delete(p, "author_id")
	c.JSON(200, p)
}
func inputValid(in postInput) bool {
	return (in.Kind == "lost" || in.Kind == "found") && validText(in.Item, 1, 100) && validText(in.Description, 5, 3000)
}
func validPrivateDetail(kind string, detail *string) bool {
	return detail == nil || strings.TrimSpace(*detail) == "" || (kind == "found" && validText(strings.TrimSpace(*detail), 1, 300))
}
func cleanPrivateDetail(detail *string) *string {
	if detail == nil || strings.TrimSpace(*detail) == "" {
		return nil
	}
	value := strings.TrimSpace(*detail)
	return &value
}
func (s *server) createPost(c *gin.Context) {
	var in postInput
	if !bind(c, &in) {
		return
	}
	if !inputValid(in) {
		fail(c, 422, "内容格式无效")
		return
	}
	if !validPrivateDetail(in.Kind, in.PrivateDetail) {
		fail(c, 422, "私密特征仅可用于拾获公告，且不超过 300 字")
		return
	}
	status := "寻找中"
	if in.Kind == "found" {
		status = "待认领"
	}
	u := current(c)
	var id int64
	e := s.db.QueryRow(c, `INSERT INTO posts(author_id,kind,item_name,description,category,location,event_time,lifecycle_status,private_verification_detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, u.ID, in.Kind, strings.TrimSpace(in.Item), strings.TrimSpace(in.Description), in.Category, in.Location, in.EventTime, status, cleanPrivateDetail(in.PrivateDetail)).Scan(&id)
	if e != nil {
		fail(c, 500, "发布失败")
		return
	}
	p, _ := s.postByID(c, id, false)
	c.JSON(201, p)
}
func (s *server) myPosts(c *gin.Context) {
	u := current(c)
	rows, e := s.db.Query(c, "SELECT id FROM posts WHERE author_id=$1 ORDER BY created_at DESC", u.ID)
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		p, err := s.postByID(c, id, false)
		if err != nil {
			continue
		}
		var rev gin.H
		var r gin.H = gin.H{}
		var item, desc string
		var cat, loc, ev, reason, privateDetail *string
		var mod string
		var created time.Time
		err = s.db.QueryRow(c, "SELECT item_name,description,category,location,event_time,moderation_status,rejection_reason,created_at,private_verification_detail FROM revisions WHERE post_id=$1", id).Scan(&item, &desc, &cat, &loc, &ev, &mod, &reason, &created, &privateDetail)
		if err == nil {
			r = gin.H{"post_id": id, "item_name": item, "description": desc, "category": cat, "location": loc, "event_time": ev, "private_verification_detail": privateDetail, "moderation_status": mod, "rejection_reason": reason, "created_at": created}
			rev = r
		}
		if rev != nil && mod == "待审核" {
			p["pending_revision"] = rev
		} else {
			p["pending_revision"] = nil
		}
		p["latest_revision"] = rev
		p["author_student_number"] = u.Student
		p["author_student_number_verified"] = false
		items = append(items, p)
	}
	c.JSON(200, items)
}
func (s *server) updatePost(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	var in postInput
	if !bind(c, &in) {
		return
	}
	if !inputValid(in) {
		fail(c, 422, "内容格式无效")
		return
	}
	if !validPrivateDetail(in.Kind, in.PrivateDetail) {
		fail(c, 422, "私密特征仅可用于拾获公告，且不超过 300 字")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "更新失败")
		return
	}
	defer tx.Rollback(c)
	var authorID int64
	var kind, moderation string
	e = tx.QueryRow(c, "SELECT author_id,kind,moderation_status FROM posts WHERE id=$1 FOR UPDATE", id).Scan(&authorID, &kind, &moderation)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if authorID != current(c).ID {
		fail(c, 403, "只能编辑自己的内容")
		return
	}
	if kind != in.Kind {
		fail(c, 422, "不能更改内容类型")
		return
	}
	if moderation == "已通过" {
		_, e = tx.Exec(c, `INSERT INTO revisions(post_id,item_name,description,category,location,event_time,private_verification_detail,moderation_status,rejection_reason,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'待审核',NULL,now()) ON CONFLICT(post_id) DO UPDATE SET item_name=EXCLUDED.item_name,description=EXCLUDED.description,category=EXCLUDED.category,location=EXCLUDED.location,event_time=EXCLUDED.event_time,private_verification_detail=EXCLUDED.private_verification_detail,moderation_status='待审核',rejection_reason=NULL,created_at=now()`, id, strings.TrimSpace(in.Item), strings.TrimSpace(in.Description), in.Category, in.Location, in.EventTime, cleanPrivateDetail(in.PrivateDetail))
	} else {
		_, e = tx.Exec(c, `UPDATE posts SET item_name=$2,description=$3,category=$4,location=$5,event_time=$6,private_verification_detail=$7,moderation_status='待审核',rejection_reason=NULL WHERE id=$1`, id, strings.TrimSpace(in.Item), strings.TrimSpace(in.Description), in.Category, in.Location, in.EventTime, cleanPrivateDetail(in.PrivateDetail))
	}
	if e != nil {
		fail(c, 500, "更新失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "更新失败")
		return
	}
	p, _ := s.postByID(c, id, false)
	c.JSON(200, p)
}
func (s *server) setPostStatus(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !bind(c, &in) {
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	defer tx.Rollback(c)
	var authorID int64
	var kind, oldStatus string
	e = tx.QueryRow(c, "SELECT author_id,kind,lifecycle_status FROM posts WHERE id=$1 FOR UPDATE", id).Scan(&authorID, &kind, &oldStatus)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if authorID != current(c).ID {
		fail(c, 403, "只能更新自己的内容")
		return
	}
	valid := []string{"已结束"}
	active, resolved := "寻找中", "已找回"
	if kind == "found" {
		active, resolved = "待认领", "已归还"
	}
	valid = append(valid, active, resolved)
	if !contains(valid, in.Status) {
		fail(c, 422, "无效的内容状态")
		return
	}
	if oldStatus != active && in.Status == active {
		fail(c, 409, "已解决或结束的内容不能重新开放")
		return
	}
	_, e = tx.Exec(c, "UPDATE posts SET lifecycle_status=$2 WHERE id=$1", id, in.Status)
	if in.Status == resolved || in.Status == "已结束" {
		if e == nil {
			_, e = tx.Exec(c, "UPDATE requests SET status='已拒绝',resolution_reason=$2 WHERE post_id=$1 AND status='待处理'", id, "关联内容已更新为「"+in.Status+"」")
		}
	}
	if e != nil || tx.Commit(c) != nil {
		fail(c, 500, "处理失败")
		return
	}
	p, _ := s.postByID(c, id, false)
	c.JSON(200, p)
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func (s *server) deletePost(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "删除失败")
		return
	}
	defer tx.Rollback(c)
	var authorID int64
	if e = tx.QueryRow(c, "SELECT author_id FROM posts WHERE id=$1 FOR UPDATE", id).Scan(&authorID); e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if authorID != current(c).ID {
		fail(c, 403, "只能删除自己的内容")
		return
	}
	var count int
	if e = tx.QueryRow(c, "SELECT (SELECT count(*) FROM requests WHERE post_id=$1)+(SELECT count(*) FROM reports WHERE target_type='post' AND target_id=$1)", id).Scan(&count); e != nil {
		fail(c, 500, "删除失败")
		return
	}
	if count > 0 {
		_, e = tx.Exec(c, "UPDATE posts SET withdrawn=true WHERE id=$1", id)
	} else {
		_, e = tx.Exec(c, "DELETE FROM posts WHERE id=$1", id)
	}
	if e != nil || tx.Commit(c) != nil {
		fail(c, 500, "删除失败")
		return
	}
	c.JSON(200, gin.H{"ok": true, "withdrawn": count > 0})
}

type requestRow struct {
	ID                 int64     `json:"id"`
	PostID             int64     `json:"post_id"`
	Kind               string    `json:"kind"`
	Explanation        string    `json:"explanation"`
	Contact            string    `json:"contact_method"`
	VerificationAnswer *string   `json:"verification_answer,omitempty"`
	Status             string    `json:"status"`
	Reason             *string   `json:"resolution_reason"`
	Created            time.Time `json:"created_at"`
	Item               *string   `json:"item_name,omitempty"`
	Nickname           *string   `json:"requester_nickname,omitempty"`
	RequesterID        int64     `json:"-"`
	AuthorID           int64     `json:"-"`
	PostKind           string    `json:"-"`
	Removed            bool      `json:"-"`
}

func (s *server) getRequest(c *gin.Context, id int64) (requestRow, error) {
	var r requestRow
	e := s.db.QueryRow(c, `SELECT q.id,q.post_id,q.kind,q.explanation,q.contact_method,q.status,q.resolution_reason,q.created_at,q.requester_id,p.author_id,p.kind,q.removed,q.verification_answer FROM requests q JOIN posts p ON p.id=q.post_id WHERE q.id=$1`, id).Scan(&r.ID, &r.PostID, &r.Kind, &r.Explanation, &r.Contact, &r.Status, &r.Reason, &r.Created, &r.RequesterID, &r.AuthorID, &r.PostKind, &r.Removed, &r.VerificationAnswer)
	if r.Removed && e == nil {
		return r, pgx.ErrNoRows
	}
	return r, e
}
func (s *server) createRequest(c *gin.Context) {
	postID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u := current(c)
	var in struct {
		Explanation        string `json:"explanation"`
		Contact            string `json:"contact_method"`
		VerificationAnswer string `json:"verification_answer"`
	}
	if !bind(c, &in) {
		return
	}
	if !validText(in.Explanation, 5, 2000) || !validText(in.Contact, 2, 120) {
		fail(c, 422, "请求信息格式无效")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "提交失败")
		return
	}
	defer tx.Rollback(c)
	var authorID int64
	var kind, lifecycle, moderation string
	var withdrawn bool
	var privateDetail *string
	e = tx.QueryRow(c, "SELECT author_id,kind,lifecycle_status,moderation_status,withdrawn,private_verification_detail FROM posts WHERE id=$1 FOR UPDATE", postID).Scan(&authorID, &kind, &lifecycle, &moderation, &withdrawn, &privateDetail)
	if e != nil || moderation != "已通过" || withdrawn {
		fail(c, 404, "内容不存在")
		return
	}
	if authorID == u.ID {
		fail(c, 422, "不能对自己的内容提交请求")
		return
	}
	if lifecycle == "已找回" || lifecycle == "已归还" || lifecycle == "已结束" {
		fail(c, 409, "该内容已结束处理")
		return
	}
	answer := strings.TrimSpace(in.VerificationAnswer)
	requiresAnswer := kind == "found" && privateDetail != nil && strings.TrimSpace(*privateDetail) != ""
	if requiresAnswer && !validText(answer, 1, 500) {
		fail(c, 422, "请回答发布者设置的私密核验问题")
		return
	}
	var answerValue *string
	if requiresAnswer && validText(answer, 1, 500) {
		answerValue = &answer
	}
	requestKind := "lead"
	if kind == "found" {
		requestKind = "claim"
	}
	var id int64
	e = tx.QueryRow(c, "INSERT INTO requests(post_id,requester_id,kind,explanation,contact_method,verification_answer) VALUES($1,$2,$3,$4,$5,$6) RETURNING id", postID, u.ID, requestKind, strings.TrimSpace(in.Explanation), strings.TrimSpace(in.Contact), answerValue).Scan(&id)
	if e != nil {
		fail(c, 500, "提交失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "提交失败")
		return
	}
	r, _ := s.getRequest(c, id)
	c.JSON(201, r)
}
func (s *server) myRequests(c *gin.Context) {
	u := current(c)
	rows, e := s.db.Query(c, `SELECT q.id,q.post_id,q.kind,q.explanation,q.contact_method,q.status,q.resolution_reason,q.created_at,p.item_name,q.verification_answer FROM requests q JOIN posts p ON p.id=q.post_id WHERE q.requester_id=$1 AND q.removed=false ORDER BY q.created_at DESC`, u.ID)
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var r requestRow
		var item string
		if e = rows.Scan(&r.ID, &r.PostID, &r.Kind, &r.Explanation, &r.Contact, &r.Status, &r.Reason, &r.Created, &item, &r.VerificationAnswer); e != nil {
			fail(c, 500, "查询失败")
			return
		}
		out = append(out, gin.H{"id": r.ID, "post_id": r.PostID, "kind": r.Kind, "explanation": r.Explanation, "contact_method": r.Contact, "verification_answer": r.VerificationAnswer, "status": r.Status, "resolution_reason": r.Reason, "created_at": r.Created, "item_name": item})
	}
	if e = rows.Err(); e != nil {
		fail(c, 500, "查询失败")
		return
	}
	c.JSON(200, out)
}
func (s *server) postRequests(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	p, e := s.postByID(c, id, false)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if p["author_id"] != current(c).ID {
		fail(c, 403, "只有内容发布者能查看请求")
		return
	}
	rows, e := s.db.Query(c, `SELECT q.id,q.post_id,q.kind,q.explanation,q.contact_method,q.status,q.resolution_reason,q.created_at,q.requester_id,u.nickname,q.verification_answer FROM requests q JOIN users u ON u.id=q.requester_id WHERE q.post_id=$1 AND q.removed=false ORDER BY q.created_at DESC`, id)
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var r requestRow
		var uid int64
		var nickname string
		if e = rows.Scan(&r.ID, &r.PostID, &r.Kind, &r.Explanation, &r.Contact, &r.Status, &r.Reason, &r.Created, &uid, &nickname, &r.VerificationAnswer); e != nil {
			fail(c, 500, "查询失败")
			return
		}
		out = append(out, gin.H{"id": r.ID, "post_id": r.PostID, "kind": r.Kind, "explanation": r.Explanation, "contact_method": r.Contact, "verification_answer": r.VerificationAnswer, "status": r.Status, "resolution_reason": r.Reason, "created_at": r.Created, "requester_nickname": nickname})
	}
	if e = rows.Err(); e != nil {
		fail(c, 500, "查询失败")
		return
	}
	c.JSON(200, out)
}
func (s *server) requestDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, e := s.getRequest(c, id)
	if e != nil {
		fail(c, 404, "请求不存在")
		return
	}
	u := current(c)
	if u.ID != r.RequesterID && u.ID != r.AuthorID {
		fail(c, 403, "没有权限查看此请求")
		return
	}
	c.JSON(200, r)
}
func (s *server) withdrawRequest(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, e := s.getRequest(c, id)
	if e != nil {
		fail(c, 404, "请求不存在")
		return
	}
	if r.RequesterID != current(c).ID {
		fail(c, 403, "只能撤回自己的请求")
		return
	}
	if r.Status != "待处理" {
		fail(c, 409, "只有待处理的请求可以撤回")
		return
	}
	result, e := s.db.Exec(c, "UPDATE requests SET status='已撤回' WHERE id=$1 AND requester_id=$2 AND status='待处理' AND removed=false", id, current(c).ID)
	if e != nil {
		fail(c, 500, "撤回失败")
		return
	}
	if result.RowsAffected() == 0 {
		fail(c, 409, "此请求已处理")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *server) acceptRequest(c *gin.Context) { s.resolveRequest(c, true) }
func (s *server) rejectRequest(c *gin.Context) { s.resolveRequest(c, false) }
func (s *server) resolveRequest(c *gin.Context, accept bool) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, e := s.getRequest(c, id)
	if e != nil {
		fail(c, 404, "请求不存在")
		return
	}
	if r.AuthorID != current(c).ID {
		fail(c, 403, "只有内容发布者能处理请求")
		return
	}
	if r.Status != "待处理" {
		fail(c, 409, "此请求已处理")
		return
	}
	reason := ""
	if !accept {
		var in struct {
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		if !validText(in.Reason, 3, 1000) {
			fail(c, 422, "处理原因至少 3 字")
			return
		}
		reason = in.Reason
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	defer tx.Rollback(c)
	var lifecycle string
	if accept {
		if e = tx.QueryRow(c, "SELECT lifecycle_status FROM posts WHERE id=$1 FOR UPDATE", r.PostID).Scan(&lifecycle); e != nil {
			fail(c, 404, "内容不存在")
			return
		}
		if lifecycle == "已找回" || lifecycle == "已归还" || lifecycle == "已结束" {
			fail(c, 409, "关联内容已结束处理")
			return
		}
	}
	var state string
	if e = tx.QueryRow(c, "SELECT status FROM requests WHERE id=$1 FOR UPDATE", id).Scan(&state); e != nil {
		fail(c, 404, "请求不存在")
		return
	}
	if state != "待处理" {
		fail(c, 409, "此请求已处理")
		return
	}
	if accept {
		_, e = tx.Exec(c, "UPDATE requests SET status='已接受' WHERE id=$1", id)
		status := "已找回"
		if r.PostKind == "found" {
			status = "已归还"
		}
		if e == nil {
			_, e = tx.Exec(c, "UPDATE posts SET lifecycle_status=$2 WHERE id=$1", r.PostID, status)
		}
		if e == nil {
			_, e = tx.Exec(c, "UPDATE requests SET status='已拒绝',resolution_reason='已有请求被接受' WHERE post_id=$1 AND id<>$2 AND status='待处理'", r.PostID, id)
		}
	} else {
		_, e = tx.Exec(c, "UPDATE requests SET status='已拒绝',resolution_reason=$2 WHERE id=$1", id, reason)
	}
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "处理失败")
		return
	}
	out, _ := s.getRequest(c, id)
	c.JSON(200, out)
}
func (s *server) report(c *gin.Context, targetType string, targetID int64) {
	u := current(c)
	var in struct {
		Explanation string `json:"explanation"`
	}
	if !bind(c, &in) {
		return
	}
	if !validText(in.Explanation, 5, 2000) {
		fail(c, 422, "举报说明至少 5 字")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "举报失败")
		return
	}
	defer tx.Rollback(c)
	if targetType == "post" {
		var authorID int64
		var moderation string
		var withdrawn bool
		e = tx.QueryRow(c, "SELECT author_id,moderation_status,withdrawn FROM posts WHERE id=$1 FOR UPDATE", targetID).Scan(&authorID, &moderation, &withdrawn)
		if e != nil || moderation != "已通过" || withdrawn {
			fail(c, 404, "内容不存在")
			return
		}
		if authorID == u.ID {
			fail(c, 422, "不能举报自己的内容")
			return
		}
	} else {
		var requesterID, authorID int64
		var removed bool
		e = tx.QueryRow(c, `SELECT q.requester_id,p.author_id,q.removed FROM requests q JOIN posts p ON p.id=q.post_id WHERE q.id=$1 FOR UPDATE OF q`, targetID).Scan(&requesterID, &authorID, &removed)
		if e != nil || removed {
			fail(c, 404, "请求不存在")
			return
		}
		if u.ID != requesterID && u.ID != authorID {
			fail(c, 403, "只有请求参与者能举报私密请求")
			return
		}
	}
	var id int64
	e = tx.QueryRow(c, "INSERT INTO reports(reporter_id,target_type,target_id,explanation) VALUES($1,$2,$3,$4) RETURNING id", u.ID, targetType, targetID, strings.TrimSpace(in.Explanation)).Scan(&id)
	if e != nil {
		fail(c, 500, "举报失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "举报失败")
		return
	}
	c.JSON(201, gin.H{"id": id, "status": "待处理"})
}
func (s *server) reportPost(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.report(c, "post", id)
}
func (s *server) reportRequest(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.report(c, "request", id)
}
func (s *server) pendingReviews(c *gin.Context) {
	posts := []gin.H{}
	rows, e := s.db.Query(c, "SELECT id,author_id FROM posts WHERE moderation_status='待审核' ORDER BY created_at")
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	for rows.Next() {
		var id, uid int64
		_ = rows.Scan(&id, &uid)
		p, err := s.postByID(c, id, false)
		if err == nil {
			var student string
			_ = s.db.QueryRow(c, "SELECT student_number FROM users WHERE id=$1", uid).Scan(&student)
			p["author_student_number"] = student
			posts = append(posts, p)
		}
	}
	rows.Close()
	revs := []gin.H{}
	rr, e := s.db.Query(c, `SELECT r.post_id,r.item_name,r.description,r.category,r.location,r.event_time,r.moderation_status,r.rejection_reason,r.created_at,p.kind,p.author_id,p.lifecycle_status,p.created_at,p.approved_at,u.nickname,u.student_number FROM revisions r JOIN posts p ON p.id=r.post_id JOIN users u ON u.id=p.author_id WHERE r.moderation_status='待审核' ORDER BY r.created_at`)
	if e == nil {
		defer rr.Close()
		for rr.Next() {
			var id, author int64
			var item, desc, mod, kind, life, nick, student string
			var cat, loc, ev, reason *string
			var created, postCreated time.Time
			var approved *time.Time
			_ = rr.Scan(&id, &item, &desc, &cat, &loc, &ev, &mod, &reason, &created, &kind, &author, &life, &postCreated, &approved, &nick, &student)
			revs = append(revs, gin.H{"post_id": id, "item_name": item, "description": desc, "category": cat, "location": loc, "event_time": ev, "moderation_status": mod, "rejection_reason": reason, "created_at": created, "kind": kind, "author_id": author, "lifecycle_status": life, "approved_at": approved, "author_nickname": nick, "author_student_number": student})
		}
	}
	c.JSON(200, gin.H{"posts": posts, "revisions": revs})
}
func (s *server) approvePost(c *gin.Context) { s.review(c, true) }
func (s *server) rejectPost(c *gin.Context)  { s.review(c, false) }
func (s *server) review(c *gin.Context, approve bool) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	reason := ""
	if !approve {
		var in struct {
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		if !validText(in.Reason, 3, 1000) {
			fail(c, 422, "处理原因至少 3 字")
			return
		}
		reason = in.Reason
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	defer tx.Rollback(c)
	var moderation string
	if e = tx.QueryRow(c, "SELECT moderation_status FROM posts WHERE id=$1 FOR UPDATE", id).Scan(&moderation); e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	var rev postInput
	e = tx.QueryRow(c, "SELECT item_name,description,category,location,event_time,private_verification_detail FROM revisions WHERE post_id=$1 AND moderation_status='待审核' FOR UPDATE", id).Scan(&rev.Item, &rev.Description, &rev.Category, &rev.Location, &rev.EventTime, &rev.PrivateDetail)
	hasRev := e == nil
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		fail(c, 500, "读取审核内容失败")
		return
	}
	if !hasRev && moderation != "待审核" {
		fail(c, 409, "没有待审核的内容")
		return
	}
	if hasRev && approve {
		_, e = tx.Exec(c, `UPDATE posts SET item_name=$2,description=$3,category=$4,location=$5,event_time=$6,private_verification_detail=$7,approved_at=now() WHERE id=$1`, id, rev.Item, rev.Description, rev.Category, rev.Location, rev.EventTime, cleanPrivateDetail(rev.PrivateDetail))
		if e == nil {
			_, e = tx.Exec(c, "UPDATE revisions SET moderation_status='已通过',rejection_reason=NULL WHERE post_id=$1", id)
		}
	} else if hasRev {
		_, e = tx.Exec(c, "UPDATE revisions SET moderation_status='已驳回',rejection_reason=$2 WHERE post_id=$1", id, reason)
	} else if approve {
		_, e = tx.Exec(c, "UPDATE posts SET moderation_status='已通过',rejection_reason=NULL,approved_at=now() WHERE id=$1", id)
	} else {
		_, e = tx.Exec(c, "UPDATE posts SET moderation_status='已驳回',rejection_reason=$2 WHERE id=$1", id, reason)
	}
	action := "reject"
	if approve {
		action = "approve"
	}
	if e == nil {
		_, e = tx.Exec(c, "INSERT INTO audit_log(actor_id,action,target_type,target_id,reason) VALUES($1,$2,'post',$3,$4)", current(c).ID, action, id, reason)
	}
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "处理失败")
		return
	}
	if approve {
		p, _ := s.postByID(c, id, false)
		c.JSON(200, p)
	} else {
		c.JSON(200, gin.H{"ok": true})
	}
}
func (s *server) takeDown(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var in struct {
		Reason string `json:"reason"`
	}
	if !bind(c, &in) {
		return
	}
	if !validText(in.Reason, 3, 1000) {
		fail(c, 422, "处理原因至少 3 字")
		return
	}
	result, e := s.db.Exec(c, "UPDATE posts SET withdrawn=true WHERE id=$1", id)
	if e != nil {
		fail(c, 404, "内容不存在")
		return
	}
	if result.RowsAffected() == 0 {
		fail(c, 404, "内容不存在")
		return
	}
	_, _ = s.db.Exec(c, "INSERT INTO audit_log(actor_id,action,target_type,target_id,reason) VALUES($1,'take-down','post',$2,$3)", current(c).ID, id, in.Reason)
	c.JSON(200, gin.H{"ok": true})
}
func (s *server) adminReports(c *gin.Context) {
	rows, e := s.db.Query(c, `SELECT r.id,u.nickname,r.target_type,r.target_id,r.explanation,r.status,r.moderator_reason,r.created_at FROM reports r JOIN users u ON u.id=r.reporter_id ORDER BY (r.status='待处理') DESC,r.created_at DESC`)
	if e != nil {
		fail(c, 500, "查询失败")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, targetID int64
		var nickname, typ, explanation, status string
		var reason *string
		var created time.Time
		_ = rows.Scan(&id, &nickname, &typ, &targetID, &explanation, &status, &reason, &created)
		item := gin.H{"id": id, "reporter_nickname": nickname, "target_type": typ, "target_id": targetID, "explanation": explanation, "status": status, "moderator_reason": reason, "created_at": created}
		var target gin.H
		if typ == "post" {
			var pi int64
			var name, desc, kind string
			var withdrawn bool
			e = s.db.QueryRow(c, "SELECT id,item_name,description,kind,withdrawn FROM posts WHERE id=$1", targetID).Scan(&pi, &name, &desc, &kind, &withdrawn)
			if e == nil {
				target = gin.H{"id": pi, "item_name": name, "description": desc, "kind": kind, "withdrawn": withdrawn}
			}
		} else {
			var qi int64
			var explanation, contact, kind, qstatus, name, nick string
			var verificationAnswer, privateDetail *string
			e = s.db.QueryRow(c, `SELECT q.id,q.explanation,q.contact_method,q.kind,q.status,p.item_name,u.nickname,q.verification_answer,p.private_verification_detail FROM requests q JOIN posts p ON p.id=q.post_id JOIN users u ON u.id=q.requester_id WHERE q.id=$1`, targetID).Scan(&qi, &explanation, &contact, &kind, &qstatus, &name, &nick, &verificationAnswer, &privateDetail)
			if e == nil {
				target = gin.H{"id": qi, "explanation": explanation, "contact_method": contact, "kind": kind, "status": qstatus, "item_name": name, "requester_nickname": nick, "verification_answer": verificationAnswer, "private_verification_detail": privateDetail}
			}
		}
		item["target"] = target
		out = append(out, item)
	}
	c.JSON(200, out)
}
func (s *server) dismissReport(c *gin.Context)  { s.reportDecision(c, false) }
func (s *server) removeReported(c *gin.Context) { s.reportDecision(c, true) }
func (s *server) reportDecision(c *gin.Context, remove bool) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var in struct {
		Reason string `json:"reason"`
	}
	if !bind(c, &in) {
		return
	}
	if !validText(in.Reason, 3, 1000) {
		fail(c, 422, "处理原因至少 3 字")
		return
	}
	var typ string
	var target int64
	e := s.db.QueryRow(c, "SELECT target_type,target_id FROM reports WHERE id=$1", id).Scan(&typ, &target)
	if e != nil {
		fail(c, 404, "举报不存在")
		return
	}
	tx, e := s.db.Begin(c)
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	defer tx.Rollback(c)
	status, action := "已驳回", "dismiss-report"
	if remove {
		status, action = "已处理", "remove-reported-target"
		if typ == "post" {
			_, e = tx.Exec(c, "UPDATE posts SET withdrawn=true WHERE id=$1", target)
		} else {
			_, e = tx.Exec(c, "UPDATE requests SET removed=true,status='已撤回',resolution_reason=$2 WHERE id=$1", target, in.Reason)
		}
	}
	if e == nil {
		_, e = tx.Exec(c, "UPDATE reports SET status=$2,moderator_reason=$3 WHERE id=$1", id, status, in.Reason)
	}
	if e == nil {
		_, e = tx.Exec(c, "INSERT INTO audit_log(actor_id,action,target_type,target_id,reason) VALUES($1,$2,$3,$4,$5)", current(c).ID, action, typ, target, in.Reason)
	}
	if e != nil {
		fail(c, 500, "处理失败")
		return
	}
	if e = tx.Commit(c); e != nil {
		fail(c, 500, "处理失败")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
