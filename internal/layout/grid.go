package layout

import "fmt"

// GridSlotSpec describes the zero-based position of a slot within the grid.
type GridSlotSpec struct {
	Name    string
	Row     int
	Col     int
	RowSpan int
	ColSpan int
}

// GridRects divides the monitor rectangle into a weighted grid and returns the
// rectangle assigned to each slot. Slots that fall outside the grid or overlap
// previously placed slots are skipped.
func GridRects(monitor Rect, gaps Gaps, reserved Insets, colWeights, rowWeights []float64, slots []GridSlotSpec) (map[string]Rect, error) {
	totalCols := len(colWeights)
	totalRows := len(rowWeights)
	if totalCols == 0 {
		for _, slot := range slots {
			if cols := slot.Col + max(1, slot.ColSpan); cols > totalCols {
				totalCols = cols
			}
		}
	}
	if totalRows == 0 {
		for _, slot := range slots {
			if rows := slot.Row + max(1, slot.RowSpan); rows > totalRows {
				totalRows = rows
			}
		}
	}
	if totalCols == 0 {
		totalCols = 1
	}
	if totalRows == 0 {
		totalRows = 1
	}

	normalizedCols, err := normalizedWeights(colWeights, totalCols)
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	normalizedRows, err := normalizedWeights(rowWeights, totalRows)
	if err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	usable := reserved.ShrinkRect(monitor)
	usable.X += gaps.Outer
	usable.Y += gaps.Outer
	usable.Width -= gaps.Outer * 2
	usable.Height -= gaps.Outer * 2
	if usable.Width < 0 {
		usable.Width = 0
	}
	if usable.Height < 0 {
		usable.Height = 0
	}

	colWidths := make([]float64, totalCols)
	rowHeights := make([]float64, totalRows)

	innerGapTotalX := gaps.Inner * float64(totalCols-1)
	innerGapTotalY := gaps.Inner * float64(totalRows-1)
	availableWidth := usable.Width - innerGapTotalX
	availableHeight := usable.Height - innerGapTotalY
	if availableWidth < 0 {
		availableWidth = 0
	}
	if availableHeight < 0 {
		availableHeight = 0
	}
	for i := 0; i < totalCols; i++ {
		colWidths[i] = availableWidth * normalizedCols[i]
	}
	for i := 0; i < totalRows; i++ {
		rowHeights[i] = availableHeight * normalizedRows[i]
	}

	colStarts := make([]float64, totalCols)
	rowStarts := make([]float64, totalRows)
	currentX := usable.X
	for i := 0; i < totalCols; i++ {
		colStarts[i] = currentX
		currentX += colWidths[i]
		if i < totalCols-1 {
			currentX += gaps.Inner
		}
	}
	currentY := usable.Y
	for i := 0; i < totalRows; i++ {
		rowStarts[i] = currentY
		currentY += rowHeights[i]
		if i < totalRows-1 {
			currentY += gaps.Inner
		}
	}

	occupied := make([][]string, totalRows)
	for r := 0; r < totalRows; r++ {
		occupied[r] = make([]string, totalCols)
	}

	rects := make(map[string]Rect, len(slots))
	for _, slot := range slots {
		if slot.Row < 0 || slot.Col < 0 {
			continue
		}
		if slot.Row >= totalRows || slot.Col >= totalCols {
			continue
		}
		rowSpan := clampSpan(slot.RowSpan, totalRows-slot.Row)
		colSpan := clampSpan(slot.ColSpan, totalCols-slot.Col)
		if rowSpan <= 0 || colSpan <= 0 {
			continue
		}
		if hasOverlap(occupied, slot.Row, slot.Col, rowSpan, colSpan) {
			continue
		}
		markOccupied(occupied, slot.Row, slot.Col, rowSpan, colSpan, slot.Name)

		startX := colStarts[slot.Col]
		endX := startX
		for c := slot.Col; c < slot.Col+colSpan; c++ {
			endX += colWidths[c]
			if c < slot.Col+colSpan-1 {
				endX += gaps.Inner
			}
		}
		startY := rowStarts[slot.Row]
		endY := startY
		for r := slot.Row; r < slot.Row+rowSpan; r++ {
			endY += rowHeights[r]
			if r < slot.Row+rowSpan-1 {
				endY += gaps.Inner
			}
		}
		rects[slot.Name] = Rect{
			X:      startX,
			Y:      startY,
			Width:  endX - startX,
			Height: endY - startY,
		}
	}
	return rects, nil
}

func normalizedWeights(weights []float64, count int) ([]float64, error) {
	result := make([]float64, count)
	if len(weights) > 0 {
		copy(result, weights)
	}
	for i := len(weights); i < count; i++ {
		result[i] = 1
	}
	sum := 0.0
	for i := 0; i < count; i++ {
		if result[i] <= 0 {
			return nil, fmt.Errorf("weight %d must be positive", i)
		}
		sum += result[i]
	}
	if sum == 0 {
		return nil, fmt.Errorf("weights sum to zero")
	}
	for i := 0; i < count; i++ {
		result[i] = result[i] / sum
	}
	return result, nil
}

func clampSpan(span, remaining int) int {
	if remaining <= 0 {
		return 0
	}
	if span <= 0 {
		span = 1
	}
	if span > remaining {
		return remaining
	}
	return span
}

func hasOverlap(occupied [][]string, row, col, rowSpan, colSpan int) bool {
	for r := row; r < row+rowSpan; r++ {
		for c := col; c < col+colSpan; c++ {
			if occupied[r][c] != "" {
				return true
			}
		}
	}
	return false
}

func markOccupied(occupied [][]string, row, col, rowSpan, colSpan int, name string) {
	for r := row; r < row+rowSpan; r++ {
		for c := col; c < col+colSpan; c++ {
			occupied[r][c] = name
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
