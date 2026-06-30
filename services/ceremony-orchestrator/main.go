package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// CeremonyOrchestrator manages distributed key generation ceremonies.
type CeremonyOrchestrator struct {
	db     *PostgresDB
	vault  *VaultClient
	router *gin.Engine
}

// NewCeremonyOrchestrator creates a new orchestrator.
func NewCeremonyOrchestrator() *CeremonyOrchestrator {
	db := NewPostgresDB()
	vault := NewVaultClient()

	return &CeremonyOrchestrator{
		db:     db,
		vault:  vault,
		router: gin.Default(),
	}
}

// setupRoutes configures API routes.
func (co *CeremonyOrchestrator) setupRoutes() {
	co.router.POST("/ceremonies", co.createCeremony)
	co.router.GET("/ceremonies/:id", co.getCeremony)
	co.router.GET("/ceremonies/:id/status", co.getCeremonyStatus)
	co.router.POST("/ceremonies/:id/sign", co.signWithThreshold)
	co.router.GET("/health", co.health)
}

// createCeremony initiates a new DKG ceremony.
func (co *CeremonyOrchestrator) createCeremony(c *gin.Context) {
	var req CreateCeremonyRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate request
	if !IsValidChain(req.ChainID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported chain"})
		return
	}

	if !IsValidThreshold(req.N, req.K) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid threshold parameters"})
		return
	}

	// Create ceremony in DB
	ceremony := &Ceremony{
		ChainID: req.ChainID,
		N:       req.N,
		K:       req.K,
		Status:  CeremonyPending,
	}

	id, err := co.db.CreateCeremony(c.Request.Context(), ceremony)
	if err != nil {
		log.Printf("failed to create ceremony: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create ceremony"})
		return
	}

	// TODO: trigger Temporal workflow to start DKG

	c.JSON(http.StatusCreated, CreateCeremonyResponse{
		ID:      id,
		Status:  CeremonyPending,
		Message: "Ceremony initiated. Awaiting parties.",
	})
}

// getCeremony retrieves ceremony details.
func (co *CeremonyOrchestrator) getCeremony(c *gin.Context) {
	id := c.Param("id")

	ceremony, err := co.db.GetCeremony(c.Request.Context(), id)
	if err != nil {
		log.Printf("failed to get ceremony: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "ceremony not found"})
		return
	}

	c.JSON(http.StatusOK, ceremony)
}

// getCeremonyStatus returns the current status of a ceremony.
func (co *CeremonyOrchestrator) getCeremonyStatus(c *gin.Context) {
	id := c.Param("id")

	ceremony, err := co.db.GetCeremony(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ceremony not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":     id,
		"status": ceremony.Status,
		"round":  ceremony.CurrentRound,
	})
}

// signWithThreshold signs a message using threshold key from a ceremony.
func (co *CeremonyOrchestrator) signWithThreshold(c *gin.Context) {
	id := c.Param("id")

	var req SignWithThresholdRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get ceremony
	ceremony, err := co.db.GetCeremony(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ceremony not found"})
		return
	}

	if ceremony.Status != CeremonyCompleted {
		c.JSON(http.StatusConflict, gin.H{"error": "ceremony not completed"})
		return
	}

	// TODO: trigger signing workflow with selected parties

	c.JSON(http.StatusAccepted, SignWithThresholdResponse{
		RequestID: "placeholder",
		Status:    "signing",
	})
}

// health checks service health.
func (co *CeremonyOrchestrator) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Start starts the orchestrator server.
func (co *CeremonyOrchestrator) Start(port string) error {
	co.setupRoutes()
	log.Printf("Starting ceremony orchestrator on :%s", port)
	return co.router.Run(":" + port)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "7001"
	}

	co := NewCeremonyOrchestrator()
	if err := co.Start(port); err != nil {
		log.Fatalf("failed to start: %v", err)
	}
}

// PostgresDB is a placeholder for database operations.
type PostgresDB struct{}

// NewPostgresDB creates a new DB connection.
func NewPostgresDB() *PostgresDB {
	return &PostgresDB{}
}

// CreateCeremony creates a new ceremony in the database.
func (db *PostgresDB) CreateCeremony(ctx context.Context, ceremony *Ceremony) (string, error) {
	// TODO: implement
	return "", fmt.Errorf("not implemented")
}

// GetCeremony retrieves a ceremony by ID.
func (db *PostgresDB) GetCeremony(ctx context.Context, id string) (*Ceremony, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}

// VaultClient is a placeholder for Vault operations.
type VaultClient struct{}

// NewVaultClient creates a new Vault client.
func NewVaultClient() *VaultClient {
	return &VaultClient{}
}
