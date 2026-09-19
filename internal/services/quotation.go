// Package services holds integrations with external systems (currently just
// Google Sheets/Drive for the PDF quotation generator).
package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Cell layout of the master "Quotation" template's "Quote" sheet. These are
// placeholders inferred from quotation-template.md — confirm the real A1
// addresses against the live spreadsheet before relying on them, then update
// here. Kept as constants so the layout can be tweaked in one place.
const (
	quoteSheetName = "Quote"

	rangeDate        = "C4"
	rangeQuoteNumber = "C5"

	rangeBuyerCompany = "F7"
	rangeBuyerContact = "F8"
	rangeBuyerProject = "F9"
	rangeBuyerPhone   = "F10"
	rangeBuyerEmail   = "F11"

	rangeItem1Number      = "B16"
	rangeItem1Description = "C16"
	rangeItem1Image       = "D16"
	rangeItem1Quantity    = "E16"
	rangeItem1Unit        = "F16"
	rangeItem1UnitPrice   = "G16"
	rangeItem1Total       = "H16"
)

// sheetRange qualifies a bare cell address with the sheet it lives on, for
// use as a Values.BatchUpdate range.
func sheetRange(cell string) string { return quoteSheetName + "!" + cell }

// QuoteRequest carries everything needed to fill one line-item quotation.
type QuoteRequest struct {
	QuoteNumber string
	Date        string // pre-formatted, e.g. "2026-09-19"

	BuyerCompany string
	BuyerContact string
	BuyerProject string
	BuyerPhone   string
	BuyerEmail   string

	ProductName string
	SKU         string
	ImageURL    string
	Quantity    float64
	Unit        string
	UnitPrice   float64
}

// QuoteResult is what a generated quotation hands back to the caller.
type QuoteResult struct {
	SpreadsheetID string
	EditURL       string
	PDF           []byte
}

// QuotationService copies the master quotation template, fills in the
// buyer/line-item cells, and exports the result as a PDF.
type QuotationService struct {
	sheets *sheets.Service
	drive  *drive.Service
	http   *http.Client

	templateSpreadsheetID string
	quotesFolderID        string
}

// NewQuotationService authenticates against Google Sheets and Drive using a
// service account key file.
func NewQuotationService(ctx context.Context, credentialsFile, templateSpreadsheetID, quotesFolderID string) (*QuotationService, error) {
	sheetsSvc, err := sheets.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("quotation: init sheets service: %w", err)
	}
	driveSvc, err := drive.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("quotation: init drive service: %w", err)
	}

	// A plain authenticated HTTP client is also needed for the PDF export
	// endpoint, which sits outside the generated API clients.
	keyJSON, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("quotation: read credentials file: %w", err)
	}
	jwtCfg, err := google.JWTConfigFromJSON(keyJSON, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("quotation: parse credentials file: %w", err)
	}

	return &QuotationService{
		sheets:                sheetsSvc,
		drive:                 driveSvc,
		http:                  jwtCfg.Client(ctx),
		templateSpreadsheetID: templateSpreadsheetID,
		quotesFolderID:        quotesFolderID,
	}, nil
}

// GenerateQuote copies the master template into the quotes folder, fills in
// the buyer/line-item cells, and returns the copy's IDs plus its PDF export.
func (s *QuotationService) GenerateQuote(ctx context.Context, req QuoteRequest) (*QuoteResult, error) {
	copyMeta := &drive.File{
		Name:    fmt.Sprintf("Quote_%s_%s", req.QuoteNumber, req.Date),
		Parents: []string{s.quotesFolderID},
	}
	copied, err := s.drive.Files.Copy(s.templateSpreadsheetID, copyMeta).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("quotation: copy template spreadsheet: %w", err)
	}
	spreadsheetID := copied.Id

	if err := s.populate(ctx, spreadsheetID, req); err != nil {
		return nil, err
	}

	pdf, err := s.exportPDF(ctx, spreadsheetID)
	if err != nil {
		return nil, err
	}

	return &QuoteResult{
		SpreadsheetID: spreadsheetID,
		EditURL:       fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/edit", spreadsheetID),
		PDF:           pdf,
	}, nil
}

// populate writes the header, buyer and first line-item cells in one batch.
func (s *QuotationService) populate(ctx context.Context, spreadsheetID string, req QuoteRequest) error {
	description := req.ProductName
	if req.SKU != "" {
		description = fmt.Sprintf("%s (SKU: %s)", req.ProductName, req.SKU)
	}
	imageFormula := fmt.Sprintf(`=IMAGE("%s")`, req.ImageURL)
	totalFormula := fmt.Sprintf("=%s*%s", rangeItem1Quantity, rangeItem1UnitPrice)

	data := []*sheets.ValueRange{
		{Range: sheetRange(rangeDate), Values: [][]any{{req.Date}}},
		{Range: sheetRange(rangeQuoteNumber), Values: [][]any{{req.QuoteNumber}}},

		{Range: sheetRange(rangeBuyerCompany), Values: [][]any{{req.BuyerCompany}}},
		{Range: sheetRange(rangeBuyerContact), Values: [][]any{{req.BuyerContact}}},
		{Range: sheetRange(rangeBuyerProject), Values: [][]any{{req.BuyerProject}}},
		{Range: sheetRange(rangeBuyerPhone), Values: [][]any{{req.BuyerPhone}}},
		{Range: sheetRange(rangeBuyerEmail), Values: [][]any{{req.BuyerEmail}}},

		{Range: sheetRange(rangeItem1Number), Values: [][]any{{1}}},
		{Range: sheetRange(rangeItem1Description), Values: [][]any{{description}}},
		{Range: sheetRange(rangeItem1Image), Values: [][]any{{imageFormula}}},
		{Range: sheetRange(rangeItem1Quantity), Values: [][]any{{req.Quantity}}},
		{Range: sheetRange(rangeItem1Unit), Values: [][]any{{req.Unit}}},
		{Range: sheetRange(rangeItem1UnitPrice), Values: [][]any{{req.UnitPrice}}},
		{Range: sheetRange(rangeItem1Total), Values: [][]any{{totalFormula}}},
	}

	_, err := s.sheets.Spreadsheets.Values.BatchUpdate(spreadsheetID, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "USER_ENTERED",
		Data:             data,
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("quotation: populate cells: %w", err)
	}
	return nil
}

// exportPDF fetches the Sheets PDF export of the given spreadsheet using the
// same authenticated client as the Drive API.
func (s *QuotationService) exportPDF(ctx context.Context, spreadsheetID string) ([]byte, error) {
	url := fmt.Sprintf(
		"https://docs.google.com/spreadsheets/d/%s/export?format=pdf&size=A4&portrait=true&fitw=true&gridlines=false&printtitle=false&sheetnames=false&fzr=false",
		spreadsheetID,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("quotation: build export request: %w", err)
	}
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("quotation: export PDF: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("quotation: read PDF export: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quotation: PDF export returned %s", resp.Status)
	}
	return body, nil
}
