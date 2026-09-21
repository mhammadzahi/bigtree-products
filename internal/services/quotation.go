// Package services holds integrations with external systems (currently just
// Google Sheets/Drive for the PDF quotation generator).
package services

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Cell layout of the master "Quotation" template's "Quote" sheet, confirmed
// against the live spreadsheet (see cmd/inspecttemplate). Kept as constants
// so the layout can be tweaked in one place if the template changes.
const (
	quoteSheetName = "Quote"

	rangeDate        = "J5" // label "Date:" sits at H5
	rangeQuoteNumber = "J7" // label "Quotation No:" sits at H7

	rangeBuyerCompany = "I15" // label "Buyer:" at H15
	rangeBuyerContact = "I16" // label "Contact:" at H16
	rangeBuyerProject = "I17" // label "Project:" at H17
	rangeBuyerPhone   = "I18" // label "Mob:" at H18
	rangeBuyerEmail   = "I21" // label "Email:" at H21

	rangeItem1Number      = "A25"
	rangeItem1Description = "C25"
	rangeItem1Quantity    = "H25"
	rangeItem1Unit        = "J25"
	rangeItem1UnitPrice   = "K25"
	rangeItem1Total       = "L25"
)

// Where the merged "Product Image" cell (G26:G30) lands on the exported PDF's
// first page, in PDF points (measured against a real export — see the
// calibration notes in the PR/commit that added this). Google's IMAGE()
// formula would be the natural way to place the photo, but Sheets requires a
// human to click a one-time "Allow access" consent per file for any formula
// that fetches an external URL, and every generated quote is a brand-new
// file — there's no API to grant that consent, so IMAGE() is a dead end for
// automation. Stamping the photo onto the already-exported PDF sidesteps
// that consent gate entirely.
const (
	pageHeightPt = 841.89 // A4 portrait

	imageCellLeftPt   = 128.7
	imageCellTopPt    = 701.05 // distance from the page's bottom edge
	imageCellWidthPt  = 18.5
	imageCellHeightPt = 23.8
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
	// SupportsAllDrives lets this succeed when the template/folder live on a
	// Shared Drive. Service accounts have no personal storage quota, so a
	// copy into a regular "My Drive" folder fails with storageQuotaExceeded —
	// the quotes folder must be a Shared Drive the service account can write to.
	copied, err := s.drive.Files.Copy(s.templateSpreadsheetID, copyMeta).
		SupportsAllDrives(true).Context(ctx).Do()
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

	if req.ImageURL != "" {
		pdf, err = s.stampProductImage(ctx, pdf, req.ImageURL)
		if err != nil {
			return nil, err
		}
	}

	return &QuoteResult{
		SpreadsheetID: spreadsheetID,
		EditURL:       fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/edit", spreadsheetID),
		PDF:           pdf,
	}, nil
}

// populate writes the header, buyer and first line-item cells. Plain
// text/number fields go in with RAW input — buyer phone numbers commonly
// start with "+", which USER_ENTERED misreads as the start of a formula
// (producing #ERROR!). Only the line-total cell needs USER_ENTERED so Sheets
// actually evaluates the formula instead of storing literal formula text.
func (s *QuotationService) populate(ctx context.Context, spreadsheetID string, req QuoteRequest) error {
	description := req.ProductName
	if req.SKU != "" {
		description = fmt.Sprintf("%s (SKU: %s)", req.ProductName, req.SKU)
	}

	rawData := []*sheets.ValueRange{
		{Range: sheetRange(rangeDate), Values: [][]any{{req.Date}}},
		{Range: sheetRange(rangeQuoteNumber), Values: [][]any{{req.QuoteNumber}}},

		{Range: sheetRange(rangeBuyerCompany), Values: [][]any{{req.BuyerCompany}}},
		{Range: sheetRange(rangeBuyerContact), Values: [][]any{{req.BuyerContact}}},
		{Range: sheetRange(rangeBuyerProject), Values: [][]any{{req.BuyerProject}}},
		{Range: sheetRange(rangeBuyerPhone), Values: [][]any{{req.BuyerPhone}}},
		{Range: sheetRange(rangeBuyerEmail), Values: [][]any{{req.BuyerEmail}}},

		{Range: sheetRange(rangeItem1Number), Values: [][]any{{1}}},
		{Range: sheetRange(rangeItem1Description), Values: [][]any{{description}}},
		{Range: sheetRange(rangeItem1Quantity), Values: [][]any{{req.Quantity}}},
		{Range: sheetRange(rangeItem1Unit), Values: [][]any{{req.Unit}}},
		{Range: sheetRange(rangeItem1UnitPrice), Values: [][]any{{req.UnitPrice}}},
	}
	if _, err := s.sheets.Spreadsheets.Values.BatchUpdate(spreadsheetID, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             rawData,
	}).Context(ctx).Do(); err != nil {
		return fmt.Errorf("quotation: populate cells: %w", err)
	}

	totalFormula := fmt.Sprintf("=%s*%s", rangeItem1Quantity, rangeItem1UnitPrice)
	formulaData := []*sheets.ValueRange{
		{Range: sheetRange(rangeItem1Total), Values: [][]any{{totalFormula}}},
	}
	if _, err := s.sheets.Spreadsheets.Values.BatchUpdate(spreadsheetID, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "USER_ENTERED",
		Data:             formulaData,
	}).Context(ctx).Do(); err != nil {
		return fmt.Errorf("quotation: populate formula cells: %w", err)
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

// stampProductImage downloads the product photo and stamps it onto the
// exported PDF's first page at the "Product Image" cell's position, scaled
// down (preserving aspect ratio) to fit within that cell.
func (s *QuotationService) stampProductImage(ctx context.Context, pdf []byte, imageURL string) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("quotation: build image request: %w", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("quotation: fetch product image: %w", err)
	}
	defer resp.Body.Close()

	imgBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("quotation: read product image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quotation: product image fetch returned %s", resp.Status)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("quotation: decode product image dimensions: %w", err)
	}

	// pdfcpu's absolute scale treats the image's pixel dimensions as points
	// at scale 1.0, so this scale factor yields the image at imageCellWidthPt
	// wide (or shorter, if that would overflow the cell's height).
	scale := imageCellWidthPt / float64(cfg.Width)
	if h := scale * float64(cfg.Height); h > imageCellHeightPt {
		scale = imageCellHeightPt / float64(cfg.Height)
	}
	// "pos:tl" anchors the image to the page's top-left corner; the offset
	// then shifts it right/down to the cell's actual position. Without an
	// explicit rotation, pdfcpu defaults stamps to a diagonal orientation —
	// "rotation:0" keeps the photo upright.
	offsetDown := -(pageHeightPt - imageCellTopPt)
	desc := fmt.Sprintf("pos:tl, offset:%.2f %.2f, scale:%.6f abs, rotation:0", imageCellLeftPt, offsetDown, scale)

	wm, err := api.ImageWatermarkForReader(bytes.NewReader(imgBytes), desc, true, false, types.POINTS)
	if err != nil {
		return nil, fmt.Errorf("quotation: build image stamp: %w", err)
	}

	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(pdf), &out, []string{"1"}, wm, nil); err != nil {
		return nil, fmt.Errorf("quotation: stamp product image onto pdf: %w", err)
	}
	return out.Bytes(), nil
}
