package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"obs-api/internal/handler"
	"obs-api/internal/logstore"
	"obs-api/internal/router"
	"obs-api/internal/tracestore"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// 连接 obs-mysql（docker-compose 中宿主机端口 3307）
	dsn := "root:root@tcp(127.0.0.1:3307)/observability?charset=utf8mb4&parseTime=True&loc=Local"
	dsn = getEnv("OBS_MYSQL_DSN", dsn)

	var db *sql.DB
	err := withRetry("ObsMySQL", 5, 2*time.Second, func() error {
		var openErr error
		db, openErr = sql.Open("mysql", dsn)
		if openErr != nil {
			return openErr
		}
		if pingErr := db.Ping(); pingErr != nil {
			db.Close()
			return pingErr
		}
		return nil
	})
	if err != nil {
		log.Fatal("连接 obs-mysql 失败:", err)
	}
	defer db.Close()

	// 连接池参数
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Println("obs-mysql 连接成功")

	// 初始化 logstore 和 handler
	store := logstore.NewMysqlStore(db)
	lh := handler.NewLogHandler(store)
	jaegerBaseURL := getEnv("JAEGER_BASE_URL", "http://jaeger:16686")
	tp := tracestore.NewJaegerProvider(jaegerBaseURL)
	statsOptions := tracestore.DefaultStatsOptions()
	statsOptions.PerOperationLimit, err = strconv.Atoi(getEnv("TRACE_STATS_PER_OPERATION_LIMIT", "1500"))
	if err != nil {
		log.Fatal("TRACE_STATS_PER_OPERATION_LIMIT 必须是整数")
	}
	statsOptions.FocusedLimit, err = strconv.Atoi(getEnv("TRACE_STATS_FOCUSED_LIMIT", "5000"))
	if err != nil || statsOptions.PerOperationLimit < 1 || statsOptions.FocusedLimit <= statsOptions.PerOperationLimit || statsOptions.FocusedLimit > 5000 {
		log.Fatal("Trace stats 配置须满足 1 <= PER_OPERATION_LIMIT < FOCUSED_LIMIT <= 5000")
	}
	th := handler.NewTraceHandler(tp, statsOptions)
	visualOptions := tracestore.DefaultVisualOptions()
	visualOptions.CandidateLimit = statsOptions.PerOperationLimit
	visualCache := tracestore.NewVisualCache(tp, visualOptions)
	defer visualCache.Close()
	logVisualCache := logstore.NewLogVisualCache(store, logstore.DefaultLogVisualOptions())
	defer logVisualCache.Close()
	vh := handler.NewVisualHandler(visualCache, tp, getEnv("TRACE_ENTRY_SERVICE", "ops-agent-backend"), logVisualCache)

	// 注册路由
	r := router.SetupRouter(db, lh, th, vh)

	addr := getEnv("ADDR", ":8081")
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	srvErr := make(chan error, 1)
	go func() {
		log.Println("obs-api 启动，监听", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		log.Printf("收到信号 %v，开始优雅退出", sig)
	case err := <-srvErr:
		log.Println("HTTP 服务异常:", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Println("关闭 HTTP 服务失败:", err)
	}
	log.Println("obs-api 已退出")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func withRetry(operationName string, maxRetries int, delay time.Duration, fn func() error) error {
	var err error
	for i := 1; i <= maxRetries; i++ {
		if err = fn(); err == nil {
			return nil
		}
		log.Printf("%s 连接失败 (第%d/%d次重试)，错误: %v", operationName, i, maxRetries, err)
		if i < maxRetries {
			time.Sleep(delay)
		}
	}
	return fmt.Errorf("[%s] 达到最大重试次数，最终失败: %w", operationName, err)
}
