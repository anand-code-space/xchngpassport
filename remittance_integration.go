package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Common types for all remittance services
type Currency string
type TransactionStatus string
type PaymentMethod string

const (
	// Currencies
	USD Currency = "USD"
	EUR Currency = "EUR"
	GBP Currency = "GBP"
	INR Currency = "INR"
	PHP Currency = "PHP"
	MXN Currency = "MXN"
	
	// Transaction Status
	StatusPending   TransactionStatus = "PENDING"
	StatusCompleted TransactionStatus = "COMPLETED"
	StatusFailed    TransactionStatus = "FAILED"
	StatusCancelled TransactionStatus = "CANCELLED"
	
	// Payment Methods
	PaymentBankTransfer PaymentMethod = "BANK_TRANSFER"
	PaymentCard         PaymentMethod = "CARD"
	PaymentWallet       PaymentMethod = "WALLET"
	PaymentCash         PaymentMethod = "CASH"
)

// Common structures
type Recipient struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Email       string            `json:"email"`
	Phone       string            `json:"phone"`
	Address     Address           `json:"address"`
	BankDetails map[string]string `json:"bank_details,omitempty"`
}

type Address struct {
	Street      string `json:"street"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
}

type ExchangeRate struct {
	From       Currency  `json:"from"`
	To         Currency  `json:"to"`
	Rate       float64   `json:"rate"`
	Fee        float64   `json:"fee"`
	ValidUntil time.Time `json:"valid_until"`
}

type TransactionRequest struct {
	SenderID       string        `json:"sender_id"`
	Recipient      Recipient     `json:"recipient"`
	Amount         float64       `json:"amount"`
	FromCurrency   Currency      `json:"from_currency"`
	ToCurrency     Currency      `json:"to_currency"`
	PaymentMethod  PaymentMethod `json:"payment_method"`
	Purpose        string        `json:"purpose"`
	Reference      string        `json:"reference"`
}

type TransactionResponse struct {
	TransactionID string            `json:"transaction_id"`
	Status        TransactionStatus `json:"status"`
	Amount        float64           `json:"amount"`
	Fee           float64           `json:"fee"`
	ExchangeRate  float64           `json:"exchange_rate"`
	EstimatedTime string            `json:"estimated_time"`
	TrackingURL   string            `json:"tracking_url,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type RemittanceQuote struct {
	Provider      string    `json:"provider"`
	Amount        float64   `json:"amount"`
	Fee           float64   `json:"fee"`
	ExchangeRate  float64   `json:"exchange_rate"`
	TotalCost     float64   `json:"total_cost"`
	ReceivedAmount float64  `json:"received_amount"`
	EstimatedTime string    `json:"estimated_time"`
	ValidUntil    time.Time `json:"valid_until"`
}

// RemittanceProvider interface that all providers must implement
type RemittanceProvider interface {
	GetName() string
	GetSupportedCurrencies() []Currency
	GetSupportedCountries() []string
	GetQuote(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error)
	SendMoney(ctx context.Context, req TransactionRequest) (*TransactionResponse, error)
	GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionResponse, error)
	GetExchangeRates(ctx context.Context, from, to Currency) (*ExchangeRate, error)
}

// Wise (formerly TransferWise) Provider
type WiseProvider struct {
	APIKey    string
	BaseURL   string
	ProfileID string
	client    *http.Client
}

func NewWiseProvider(apiKey, profileID string) *WiseProvider {
	return &WiseProvider{
		APIKey:    apiKey,
		BaseURL:   "https://api.transferwise.com",
		ProfileID: profileID,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (w *WiseProvider) GetName() string {
	return "Wise"
}

func (w *WiseProvider) GetSupportedCurrencies() []Currency {
	return []Currency{USD, EUR, GBP, INR, PHP}
}

func (w *WiseProvider) GetSupportedCountries() []string {
	return []string{"US", "GB", "IN", "PH", "DE", "FR", "ES"}
}

func (w *WiseProvider) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}
	
	req, err := http.NewRequestWithContext(ctx, method, w.BaseURL+endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+w.APIKey)
	req.Header.Set("Content-Type", "application/json")
	
	return w.client.Do(req)
}

func (w *WiseProvider) GetQuote(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error) {
	quoteReq := map[string]interface{}{
		"profile":        w.ProfileID,
		"source":         req.FromCurrency,
		"target":         req.ToCurrency,
		"sourceAmount":   req.Amount,
		"type":           "REGULAR",
	}
	
	resp, err := w.makeRequest(ctx, "POST", "/v1/quotes", quoteReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var quoteResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&quoteResp); err != nil {
		return nil, err
	}
	
	fee := quoteResp["fee"].(float64)
	rate := quoteResp["rate"].(float64)
	targetAmount := quoteResp["targetAmount"].(float64)
	
	return &RemittanceQuote{
		Provider:       w.GetName(),
		Amount:         req.Amount,
		Fee:            fee,
		ExchangeRate:   rate,
		TotalCost:      req.Amount + fee,
		ReceivedAmount: targetAmount,
		EstimatedTime:  "1-2 business days",
		ValidUntil:     time.Now().Add(24 * time.Hour),
	}, nil
}

func (w *WiseProvider) SendMoney(ctx context.Context, req TransactionRequest) (*TransactionResponse, error) {
	// In real implementation, this would create a transfer
	transferReq := map[string]interface{}{
		"targetAccount": req.Recipient.ID,
		"quote":         "quote-id", // Would be from previous quote
		"customerTransactionId": req.Reference,
		"details": map[string]interface{}{
			"reference": req.Purpose,
		},
	}
	
	resp, err := w.makeRequest(ctx, "POST", "/v1/transfers", transferReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var transferResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&transferResp); err != nil {
		return nil, err
	}
	
	return &TransactionResponse{
		TransactionID: transferResp["id"].(string),
		Status:        StatusPending,
		Amount:        req.Amount,
		Fee:           10.0, // Would be from quote
		ExchangeRate:  1.2,  // Would be from quote
		EstimatedTime: "1-2 business days",
		TrackingURL:   fmt.Sprintf("https://wise.com/track/%s", transferResp["id"]),
	}, nil
}

func (w *WiseProvider) GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionResponse, error) {
	resp, err := w.makeRequest(ctx, "GET", "/v1/transfers/"+transactionID, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var statusResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		return nil, err
	}
	
	status := StatusPending
	if statusResp["status"].(string) == "outgoing_payment_sent" {
		status = StatusCompleted
	}
	
	return &TransactionResponse{
		TransactionID: transactionID,
		Status:        status,
		TrackingURL:   fmt.Sprintf("https://wise.com/track/%s", transactionID),
	}, nil
}

func (w *WiseProvider) GetExchangeRates(ctx context.Context, from, to Currency) (*ExchangeRate, error) {
	endpoint := fmt.Sprintf("/v1/rates?source=%s&target=%s", from, to)
	resp, err := w.makeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var rates []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rates); err != nil {
		return nil, err
	}
	
	if len(rates) == 0 {
		return nil, errors.New("no exchange rate found")
	}
	
	rate := rates[0]["rate"].(float64)
	
	return &ExchangeRate{
		From:       from,
		To:         to,
		Rate:       rate,
		Fee:        5.0, // Example fee
		ValidUntil: time.Now().Add(1 * time.Hour),
	}, nil
}

// Remitly Provider
type RemitlyProvider struct {
	APIKey  string
	BaseURL string
	client  *http.Client
}

func NewRemitlyProvider(apiKey string) *RemitlyProvider {
	return &RemitlyProvider{
		APIKey:  apiKey,
		BaseURL: "https://api.remitly.com",
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *RemitlyProvider) GetName() string {
	return "Remitly"
}

func (r *RemitlyProvider) GetSupportedCurrencies() []Currency {
	return []Currency{USD, EUR, PHP, INR, MXN}
}

func (r *RemitlyProvider) GetSupportedCountries() []string {
	return []string{"US", "PH", "IN", "MX", "GB"}
}

func (r *RemitlyProvider) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}
	
	req, err := http.NewRequestWithContext(ctx, method, r.BaseURL+endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	
	return r.client.Do(req)
}

func (r *RemitlyProvider) GetQuote(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error) {
	// Simulate Remitly quote API call
	fee := req.Amount * 0.02 // 2% fee
	rate := 1.15 // Example rate
	receivedAmount := req.Amount * rate
	
	return &RemittanceQuote{
		Provider:       r.GetName(),
		Amount:         req.Amount,
		Fee:            fee,
		ExchangeRate:   rate,
		TotalCost:      req.Amount + fee,
		ReceivedAmount: receivedAmount,
		EstimatedTime:  "Minutes to hours",
		ValidUntil:     time.Now().Add(30 * time.Minute),
	}, nil
}

func (r *RemitlyProvider) SendMoney(ctx context.Context, req TransactionRequest) (*TransactionResponse, error) {
	// Simulate Remitly transfer API call
	transactionID := fmt.Sprintf("REM_%d", time.Now().Unix())
	
	return &TransactionResponse{
		TransactionID: transactionID,
		Status:        StatusPending,
		Amount:        req.Amount,
		Fee:           req.Amount * 0.02,
		ExchangeRate:  1.15,
		EstimatedTime: "Minutes to hours",
		TrackingURL:   fmt.Sprintf("https://remitly.com/track/%s", transactionID),
	}, nil
}

func (r *RemitlyProvider) GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionResponse, error) {
	// Simulate status check
	return &TransactionResponse{
		TransactionID: transactionID,
		Status:        StatusCompleted,
		TrackingURL:   fmt.Sprintf("https://remitly.com/track/%s", transactionID),
	}, nil
}

func (r *RemitlyProvider) GetExchangeRates(ctx context.Context, from, to Currency) (*ExchangeRate, error) {
	return &ExchangeRate{
		From:       from,
		To:         to,
		Rate:       1.15, // Example rate
		Fee:        3.0,
		ValidUntil: time.Now().Add(30 * time.Minute),
	}, nil
}

// WorldRemit Provider
type WorldRemitProvider struct {
	APIKey    string
	APISecret string
	BaseURL   string
	client    *http.Client
}

func NewWorldRemitProvider(apiKey, apiSecret string) *WorldRemitProvider {
	return &WorldRemitProvider{
		APIKey:    apiKey,
		APISecret: apiSecret,
		BaseURL:   "https://api.worldremit.com",
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (wr *WorldRemitProvider) GetName() string {
	return "WorldRemit"
}

func (wr *WorldRemitProvider) GetSupportedCurrencies() []Currency {
	return []Currency{USD, EUR, GBP, INR, PHP}
}

func (wr *WorldRemitProvider) GetSupportedCountries() []string {
	return []string{"US", "GB", "IN", "PH", "KE", "GH"}
}

func (wr *WorldRemitProvider) generateSignature(method, endpoint, timestamp, body string) string {
	message := method + "\n" + endpoint + "\n" + timestamp + "\n" + body
	h := hmac.New(sha256.New, []byte(wr.APISecret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

func (wr *WorldRemitProvider) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	var reqBody string
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = string(jsonBody)
	}
	
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := wr.generateSignature(method, endpoint, timestamp, reqBody)
	
	req, err := http.NewRequestWithContext(ctx, method, wr.BaseURL+endpoint, strings.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("X-API-Key", wr.APIKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", signature)
	req.Header.Set("Content-Type", "application/json")
	
	return wr.client.Do(req)
}

func (wr *WorldRemitProvider) GetQuote(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error) {
	// Simulate WorldRemit quote
	fee := 5.99 // Fixed fee
	rate := 1.18
	receivedAmount := req.Amount * rate
	
	return &RemittanceQuote{
		Provider:       wr.GetName(),
		Amount:         req.Amount,
		Fee:            fee,
		ExchangeRate:   rate,
		TotalCost:      req.Amount + fee,
		ReceivedAmount: receivedAmount,
		EstimatedTime:  "Minutes",
		ValidUntil:     time.Now().Add(15 * time.Minute),
	}, nil
}

func (wr *WorldRemitProvider) SendMoney(ctx context.Context, req TransactionRequest) (*TransactionResponse, error) {
	transactionID := fmt.Sprintf("WR_%d", time.Now().Unix())
	
	return &TransactionResponse{
		TransactionID: transactionID,
		Status:        StatusPending,
		Amount:        req.Amount,
		Fee:           5.99,
		ExchangeRate:  1.18,
		EstimatedTime: "Minutes",
		TrackingURL:   fmt.Sprintf("https://worldremit.com/track/%s", transactionID),
	}, nil
}

func (wr *WorldRemitProvider) GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionResponse, error) {
	return &TransactionResponse{
		TransactionID: transactionID,
		Status:        StatusCompleted,
		TrackingURL:   fmt.Sprintf("https://worldremit.com/track/%s", transactionID),
	}, nil
}

func (wr *WorldRemitProvider) GetExchangeRates(ctx context.Context, from, to Currency) (*ExchangeRate, error) {
	return &ExchangeRate{
		From:       from,
		To:         to,
		Rate:       1.18,
		Fee:        5.99,
		ValidUntil: time.Now().Add(15 * time.Minute),
	}, nil
}

// Remittance Hub - Main orchestrator
type RemittanceHub struct {
	providers []RemittanceProvider
}

func NewRemittanceHub() *RemittanceHub {
	return &RemittanceHub{
		providers: make([]RemittanceProvider, 0),
	}
}

func (rh *RemittanceHub) AddProvider(provider RemittanceProvider) {
	rh.providers = append(rh.providers, provider)
}

func (rh *RemittanceHub) GetAvailableProviders(fromCountry, toCountry string, fromCurrency, toCurrency Currency) []RemittanceProvider {
	var available []RemittanceProvider
	
	for _, provider := range rh.providers {
		// Check if provider supports the currencies
		supportsCurrencies := false
		for _, currency := range provider.GetSupportedCurrencies() {
			if currency == fromCurrency || currency == toCurrency {
				supportsCurrencies = true
				break
			}
		}
		
		// Check if provider supports the countries
		supportsCountries := false
		for _, country := range provider.GetSupportedCountries() {
			if country == fromCountry || country == toCountry {
				supportsCountries = true
				break
			}
		}
		
		if supportsCurrencies && supportsCountries {
			available = append(available, provider)
		}
	}
	
	return available
}

func (rh *RemittanceHub) GetQuotes(ctx context.Context, req TransactionRequest) ([]*RemittanceQuote, error) {
	providers := rh.GetAvailableProviders("US", req.Recipient.Address.CountryCode, req.FromCurrency, req.ToCurrency)
	quotes := make([]*RemittanceQuote, 0, len(providers))
	
	for _, provider := range providers {
		quote, err := provider.GetQuote(ctx, req)
		if err != nil {
			log.Printf("Error getting quote from %s: %v", provider.GetName(), err)
			continue
		}
		quotes = append(quotes, quote)
	}
	
	// Sort quotes by total cost (best value first)
	sort.Slice(quotes, func(i, j int) bool {
		return quotes[i].TotalCost < quotes[j].TotalCost
	})
	
	return quotes, nil
}

func (rh *RemittanceHub) SendMoneyWithProvider(ctx context.Context, providerName string, req TransactionRequest) (*TransactionResponse, error) {
	for _, provider := range rh.providers {
		if provider.GetName() == providerName {
			return provider.SendMoney(ctx, req)
		}
	}
	return nil, fmt.Errorf("provider %s not found", providerName)
}

func (rh *RemittanceHub) GetBestQuote(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error) {
	quotes, err := rh.GetQuotes(ctx, req)
	if err != nil {
		return nil, err
	}
	
	if len(quotes) == 0 {
		return nil, errors.New("no quotes available")
	}
	
	return quotes[0], nil // First quote is best due to sorting
}

// Wallet Service Integration
type WalletRemittanceService struct {
	hub *RemittanceHub
}

func NewWalletRemittanceService() *WalletRemittanceService {
	hub := NewRemittanceHub()
	
	// Add providers
	hub.AddProvider(NewWiseProvider("wise-api-key", "wise-profile-id"))
	hub.AddProvider(NewRemitlyProvider("remitly-api-key"))
	hub.AddProvider(NewWorldRemitProvider("worldremit-api-key", "worldremit-secret"))
	
	return &WalletRemittanceService{hub: hub}
}

func (wrs *WalletRemittanceService) GetRemittanceOptions(ctx context.Context, req TransactionRequest) ([]*RemittanceQuote, error) {
	return wrs.hub.GetQuotes(ctx, req)
}

func (wrs *WalletRemittanceService) SendRemittance(ctx context.Context, providerName string, req TransactionRequest) (*TransactionResponse, error) {
	return wrs.hub.SendMoneyWithProvider(ctx, providerName, req)
}

func (wrs *WalletRemittanceService) GetBestOption(ctx context.Context, req TransactionRequest) (*RemittanceQuote, error) {
	return wrs.hub.GetBestQuote(ctx, req)
}

// Example usage and demo
func main() {
	ctx := context.Background()
	
	// Create wallet remittance service
	service := NewWalletRemittanceService()
	
	// Create sample transaction request
	recipient := Recipient{
		ID:    "recipient-123",
		Name:  "John Doe",
		Email: "john@example.com",
		Phone: "+1234567890",
		Address: Address{
			Street:      "123 Main St",
			City:        "Manila",
			Country:     "Philippines",
			CountryCode: "PH",
		},
	}
	
	request := TransactionRequest{
		SenderID:      "sender-456",
		Recipient:     recipient,
		Amount:        1000.0,
		FromCurrency:  USD,
		ToCurrency:    PHP,
		PaymentMethod: PaymentBankTransfer,
		Purpose:       "Family support",
		Reference:     "REF-001",
	}
	
	// Get all available remittance options
	fmt.Println("=== Available Remittance Options ===")
	quotes, err := service.GetRemittanceOptions(ctx, request)
	if err != nil {
		log.Fatal("Error getting quotes:", err)
	}
	
	for i, quote := range quotes {
		fmt.Printf("\nOption %d - %s:\n", i+1, quote.Provider)
		fmt.Printf("  Send Amount: $%.2f %s\n", quote.Amount, request.FromCurrency)
		fmt.Printf("  Fee: $%.2f\n", quote.Fee)
		fmt.Printf("  Exchange Rate: %.4f\n", quote.ExchangeRate)
		fmt.Printf("  Total Cost: $%.2f\n", quote.TotalCost)
		fmt.Printf("  Recipient Gets: %.2f %s\n", quote.ReceivedAmount, request.ToCurrency)
		fmt.Printf("  Estimated Time: %s\n", quote.EstimatedTime)
		fmt.Printf("  Valid Until: %s\n", quote.ValidUntil.Format("2006-01-02 15:04:05"))
	}
	
	// Get best option
	fmt.Println("\n=== Best Option ===")
	bestQuote, err := service.GetBestOption(ctx, request)
	if err != nil {
		log.Fatal("Error getting best quote:", err)
	}
	
	fmt.Printf("Best Provider: %s\n", bestQuote.Provider)
	fmt.Printf("Total Cost: $%.2f\n", bestQuote.TotalCost)
	fmt.Printf("Recipient Gets: %.2f %s\n", bestQuote.ReceivedAmount, request.ToCurrency)
	
	// Send money with best provider
	fmt.Println("\n=== Sending Money ===")
	transaction, err := service.SendRemittance(ctx, bestQuote.Provider, request)
	if err != nil {
		log.Fatal("Error sending money:", err)
	}
	
	fmt.Printf("Transaction ID: %s\n", transaction.TransactionID)
	fmt.Printf("Status: %s\n", transaction.Status)
	fmt.Printf("Tracking URL: %s\n", transaction.TrackingURL)
	fmt.Printf("Estimated Delivery: %s\n", transaction.EstimatedTime)
	
	fmt.Println("\n=== Integration Complete ===")
}