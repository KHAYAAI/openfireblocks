package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.temporal.io/sdk/client"
)

// CeremonyOrchestrator manages distributed key generation ceremonies.
type CeremonyOrchestrator struct {
	db              *PostgresDB
	vault           *VaultClient
	temporalClient  client.Client
	temporalTaskQueue string
	router          *gin.Engine
}

// NewCeremonyOrchestrator creates a new orchestrator.
func NewCeremonyOrchestrator(temporalClient client.Client) *CeremonyOrchestrator {
	db := NewPostgresDB()
	vault := NewVaultClient()

	return &CeremonyOrchestrator{
		db:                db,
		vault:             vault,
		temporalClient:    temporalClient,
		temporalTaskQueue: "transaction-settlement",
		router:            gin.Default(),
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

	// Extract customer ID from request context (set by auth middleware)
	customerID, ok := c.Get("customerId")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing customer ID"})
		return
	}

	// Create ceremony in DB
	ceremony := &Ceremony{
		CustomerID: customerID.(string),
		ChainID:    req.ChainID,
		N:          req.N,
		K:          req.K,
		Status:     CeremonyPending,
	}

	id, err := co.db.CreateCeremony(c.Request.Context(), ceremony)
	if err != nil {
		log.Printf("failed to create ceremony: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create ceremony"})
		return
	}

	// Start Temporal DKG workflow
	workflowOptions := client.StartWorkflowOptions{
		ID:        "ceremony_" + id,
		TaskQueue: co.temporalTaskQueue,
	}

	dkgReq := map[string]interface{}{
		"customerId":    customerID.(string),
		"ceremonyId":    id,
		"chainId":       req.ChainID,
		"n":             req.N,
		"k":             req.K,
		"partyIds":      req.PartyIDs,
		"partyEndpoints": req.PartyEndpoints,
	}

	we, err := co.temporalClient.ExecuteWorkflow(c.Request.Context(), workflowOptions, "DKGCeremonyWorkflow", dkgReq)
	if err != nil {
		log.Printf("failed to start DKG workflow: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start ceremony"})
		return
	}

	log.Printf("DKG workflow started: %s", we.GetID())

	c.JSON(http.StatusCreated, CreateCeremonyResponse{
		ID:      id,
		Status:  CeremonyPending,
		Message: "Ceremony initiated. DKG workflow started.",
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

	// Connect to Temporal
	temporalHostPort := os.Getenv("TEMPORAL_HOSTPORT")
	if temporalHostPort == "" {
		temporalHostPort = "localhost:7233"
	}

	temporalNamespace := os.Getenv("TEMPORAL_NAMESPACE")
	if temporalNamespace == "" {
		temporalNamespace = "default"
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:  temporalHostPort,
		Namespace: temporalNamespace,
	})
	if err != nil {
		log.Fatalf("failed to connect to Temporal: %v", err)
	}
	defer temporalClient.Close()

	log.Printf("connected to Temporal at %s/%s", temporalHostPort, temporalNamespace)

	co := NewCeremonyOrchestrator(temporalClient)
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
