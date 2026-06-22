package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration loaded from env / .env file.
type Config struct {
	App      AppConfig
	Server   ServerConfig
	DB       DBConfig
	Redis    RedisConfig
	JWT      JWTConfig
	S3       S3Config
	Azure    AzureConfig
	IAP      IAPConfig
	FCM      FCMConfig
	Google   GoogleConfig
	Freemium FreemiumConfig
	Asynq    AsynqConfig
	RateLimit RateLimitConfig
	Cost     CostConfig
}

type AppConfig struct {
	Env string `mapstructure:"APP_ENV"`
}

type ServerConfig struct {
	Host string `mapstructure:"SERVER_HOST"`
	Port int    `mapstructure:"SERVER_PORT"`
}

func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

type DBConfig struct {
	Host            string        `mapstructure:"DB_HOST"`
	Port            int           `mapstructure:"DB_PORT"`
	Name            string        `mapstructure:"DB_NAME"`
	User            string        `mapstructure:"DB_USER"`
	Password        string        `mapstructure:"DB_PASSWORD"`
	PoolMaxConns    int           `mapstructure:"DB_POOL_MAX_CONNS"`
	PoolMinConns    int           `mapstructure:"DB_POOL_MIN_CONNS"`
	ConnMaxLifetime time.Duration `mapstructure:"DB_CONN_MAX_LIFETIME"`
	ConnMaxIdleTime time.Duration `mapstructure:"DB_CONN_MAX_IDLE_TIME"`
}

func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable pool_max_conns=%d pool_min_conns=%d pool_max_conn_lifetime=%s pool_max_conn_idle_time=%s",
		d.Host, d.Port, d.Name, d.User, d.Password,
		d.PoolMaxConns, d.PoolMinConns,
		d.ConnMaxLifetime.String(), d.ConnMaxIdleTime.String(),
	)
}

func (d DBConfig) MigrationURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

type RedisConfig struct {
	Addr     string `mapstructure:"REDIS_ADDR"`
	Password string `mapstructure:"REDIS_PASSWORD"`
	DB       int    `mapstructure:"REDIS_DB"`
}

type JWTConfig struct {
	AccessSecret  string        `mapstructure:"JWT_ACCESS_SECRET"`
	RefreshSecret string        `mapstructure:"JWT_REFRESH_SECRET"`
	AccessTTL     time.Duration `mapstructure:"JWT_ACCESS_TTL"`
	RefreshTTL    time.Duration `mapstructure:"JWT_REFRESH_TTL"`
}

type S3Config struct {
	Endpoint     string `mapstructure:"S3_ENDPOINT"`
	Bucket       string `mapstructure:"S3_BUCKET_AUDIO"`
	AccessKey    string `mapstructure:"S3_ACCESS_KEY"`
	SecretKey    string `mapstructure:"S3_SECRET_KEY"`
	Region       string `mapstructure:"S3_REGION"`
	UsePathStyle bool   `mapstructure:"S3_USE_PATH_STYLE"`
}

type AzureConfig struct {
	SpeechKey      string        `mapstructure:"AZURE_SPEECH_KEY"`
	SpeechRegion   string        `mapstructure:"AZURE_SPEECH_REGION"`
	SpeechTokenTTL time.Duration `mapstructure:"AZURE_SPEECH_TOKEN_TTL"`
	TTSKey         string        `mapstructure:"AZURE_TTS_KEY"`
	TTSRegion      string        `mapstructure:"AZURE_TTS_REGION"`
	SpeechaceKey   string        `mapstructure:"SPEECHACE_API_KEY"`
}

type IAPConfig struct {
	AppleSharedSecret  string `mapstructure:"APPLE_IAP_SHARED_SECRET"`
	GooglePackageName  string `mapstructure:"GOOGLE_IAP_PACKAGE_NAME"`
}

type FCMConfig struct {
	ServerKey string `mapstructure:"FCM_SERVER_KEY"`
}

type GoogleConfig struct {
	ClientID string `mapstructure:"GOOGLE_CLIENT_ID"`
}

type FreemiumConfig struct {
	DailyLimit int `mapstructure:"FREEMIUM_DAILY_LIMIT"`
}

type AsynqConfig struct {
	Concurrency int `mapstructure:"ASYNQ_CONCURRENCY"`
}

type RateLimitConfig struct {
	TokenPerUserPerMin int `mapstructure:"RATE_LIMIT_TOKEN_PER_USER_PER_MIN"`
	TokenPerIPPerMin   int `mapstructure:"RATE_LIMIT_TOKEN_PER_IP_PER_MIN"`
}

type CostConfig struct {
	CircuitBreakerThreshold float64 `mapstructure:"COST_CIRCUIT_BREAKER_THRESHOLD"`
}

// Load reads configuration from environment variables and optional .env file.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("APP_ENV", "development")
	v.SetDefault("SERVER_HOST", "0.0.0.0")
	v.SetDefault("SERVER_PORT", 8080)
	v.SetDefault("DB_HOST", "localhost")
	v.SetDefault("DB_PORT", 5432)
	v.SetDefault("DB_NAME", "phonara")
	v.SetDefault("DB_USER", "phonara")
	v.SetDefault("DB_PASSWORD", "phonara_secret")
	v.SetDefault("DB_POOL_MAX_CONNS", 25)
	v.SetDefault("DB_POOL_MIN_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME", "5m")
	v.SetDefault("DB_CONN_MAX_IDLE_TIME", "1m")
	v.SetDefault("REDIS_ADDR", "localhost:6379")
	v.SetDefault("REDIS_DB", 0)
	v.SetDefault("JWT_ACCESS_TTL", "15m")
	v.SetDefault("JWT_REFRESH_TTL", "168h") // 7 days
	v.SetDefault("S3_REGION", "us-east-1")
	v.SetDefault("S3_USE_PATH_STYLE", true)
	v.SetDefault("AZURE_SPEECH_REGION", "eastus")
	v.SetDefault("AZURE_SPEECH_TOKEN_TTL", "60s")
	v.SetDefault("AZURE_TTS_REGION", "eastus")
	v.SetDefault("FREEMIUM_DAILY_LIMIT", 10)
	v.SetDefault("ASYNQ_CONCURRENCY", 10)
	v.SetDefault("RATE_LIMIT_TOKEN_PER_USER_PER_MIN", 6)
	v.SetDefault("RATE_LIMIT_TOKEN_PER_IP_PER_MIN", 20)
	v.SetDefault("COST_CIRCUIT_BREAKER_THRESHOLD", 50.0)

	// Read from .env file if present
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.ReadInConfig() // ok if not found — env vars take precedence

	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Manual mapping since viper flattens env keys
	cfg.App.Env = v.GetString("APP_ENV")
	cfg.Server.Host = v.GetString("SERVER_HOST")
	cfg.Server.Port = v.GetInt("SERVER_PORT")
	cfg.DB.Host = v.GetString("DB_HOST")
	cfg.DB.Port = v.GetInt("DB_PORT")
	cfg.DB.Name = v.GetString("DB_NAME")
	cfg.DB.User = v.GetString("DB_USER")
	cfg.DB.Password = v.GetString("DB_PASSWORD")
	cfg.DB.PoolMaxConns = v.GetInt("DB_POOL_MAX_CONNS")
	cfg.DB.PoolMinConns = v.GetInt("DB_POOL_MIN_CONNS")
	cfg.DB.ConnMaxLifetime = v.GetDuration("DB_CONN_MAX_LIFETIME")
	cfg.DB.ConnMaxIdleTime = v.GetDuration("DB_CONN_MAX_IDLE_TIME")
	cfg.Redis.Addr = v.GetString("REDIS_ADDR")
	cfg.Redis.Password = v.GetString("REDIS_PASSWORD")
	cfg.Redis.DB = v.GetInt("REDIS_DB")
	cfg.JWT.AccessSecret = v.GetString("JWT_ACCESS_SECRET")
	cfg.JWT.RefreshSecret = v.GetString("JWT_REFRESH_SECRET")
	cfg.JWT.AccessTTL = v.GetDuration("JWT_ACCESS_TTL")
	cfg.JWT.RefreshTTL = v.GetDuration("JWT_REFRESH_TTL")
	cfg.S3.Endpoint = v.GetString("S3_ENDPOINT")
	cfg.S3.Bucket = v.GetString("S3_BUCKET_AUDIO")
	cfg.S3.AccessKey = v.GetString("S3_ACCESS_KEY")
	cfg.S3.SecretKey = v.GetString("S3_SECRET_KEY")
	cfg.S3.Region = v.GetString("S3_REGION")
	cfg.S3.UsePathStyle = v.GetBool("S3_USE_PATH_STYLE")
	cfg.Azure.SpeechKey = v.GetString("AZURE_SPEECH_KEY")
	cfg.Azure.SpeechRegion = v.GetString("AZURE_SPEECH_REGION")
	cfg.Azure.SpeechTokenTTL = v.GetDuration("AZURE_SPEECH_TOKEN_TTL")
	cfg.Azure.TTSKey = v.GetString("AZURE_TTS_KEY")
	cfg.Azure.TTSRegion = v.GetString("AZURE_TTS_REGION")
	cfg.Azure.SpeechaceKey = v.GetString("SPEECHACE_API_KEY")
	cfg.IAP.AppleSharedSecret = v.GetString("APPLE_IAP_SHARED_SECRET")
	cfg.IAP.GooglePackageName = v.GetString("GOOGLE_IAP_PACKAGE_NAME")
	cfg.FCM.ServerKey = v.GetString("FCM_SERVER_KEY")
	cfg.Google.ClientID = v.GetString("GOOGLE_CLIENT_ID")
	cfg.Freemium.DailyLimit = v.GetInt("FREEMIUM_DAILY_LIMIT")
	cfg.Asynq.Concurrency = v.GetInt("ASYNQ_CONCURRENCY")
	cfg.RateLimit.TokenPerUserPerMin = v.GetInt("RATE_LIMIT_TOKEN_PER_USER_PER_MIN")
	cfg.RateLimit.TokenPerIPPerMin = v.GetInt("RATE_LIMIT_TOKEN_PER_IP_PER_MIN")
	cfg.Cost.CircuitBreakerThreshold = v.GetFloat64("COST_CIRCUIT_BREAKER_THRESHOLD")

	return cfg, nil
}
