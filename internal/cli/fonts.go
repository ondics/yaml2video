package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ondics/yaml2video/internal/render"
)

func printFontCatalog(output, warnings io.Writer, catalog render.FontCatalog) error {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "FONT FAMILY\tSOURCE\tCACHED\tOFFLINE")
	yesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	for _, family := range catalog.Fonts {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", family.Family, strings.Join(family.Sources, ", "), yesNo(family.Cached), yesNo(family.AvailableOffline))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "\n%d font families. Cached Google fonts may not include every weight or optical size.\n", len(catalog.Fonts)); err != nil {
		return err
	}
	for _, warning := range catalog.Warnings {
		if _, err := fmt.Fprintf(warnings, "[Warning] %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}
