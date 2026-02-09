// Package database provides contact resolution functionality.
package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

var (
	resolver     *ContactResolver
	resolverOnce sync.Once
)

// ContactResolver resolves phone numbers and email addresses to contact names.
type ContactResolver struct {
	phoneToName map[string]string
	emailToName map[string]string
	loaded      bool
	mu          sync.RWMutex
}

// NewContactResolver creates a new ContactResolver.
func NewContactResolver() *ContactResolver {
	return &ContactResolver{
		phoneToName: make(map[string]string),
		emailToName: make(map[string]string),
	}
}

// GetContactName returns the contact name for a phone number or email.
func GetContactName(identifier string) string {
	resolverOnce.Do(func() {
		resolver = NewContactResolver()
	})
	return resolver.Resolve(identifier)
}

// PreloadContacts loads contacts into memory.
func PreloadContacts() {
	resolverOnce.Do(func() {
		resolver = NewContactResolver()
	})
	resolver.loadContacts()
}

// getAddressBookPaths finds all AddressBook database files on the system.
func getAddressBookPaths() []string {
	home, _ := os.UserHomeDir()
	basePath := filepath.Join(home, "Library", "Application Support", "AddressBook", "Sources")

	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil
	}

	var dbFiles []string
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			dbFile := filepath.Join(basePath, entry.Name(), "AddressBook-v22.abcddb")
			if _, err := os.Stat(dbFile); err == nil {
				dbFiles = append(dbFiles, dbFile)
			}
		}
	}

	return dbFiles
}

// NormalizePhoneNumber normalizes a phone number to just digits for comparison.
func NormalizePhoneNumber(phone string) string {
	if phone == "" {
		return ""
	}

	phone = strings.TrimSpace(phone)
	hasPlus := strings.HasPrefix(phone, "+")

	var digits strings.Builder
	for _, c := range phone {
		if unicode.IsDigit(c) {
			digits.WriteRune(c)
		}
	}

	if hasPlus {
		return "+" + digits.String()
	}
	return digits.String()
}

// GetPhoneVariants generates common variants of a phone number for matching.
func GetPhoneVariants(phone string) []string {
	if phone == "" {
		return nil
	}

	variants := []string{phone}
	var digitsBuilder strings.Builder
	for _, c := range phone {
		if unicode.IsDigit(c) {
			digitsBuilder.WriteRune(c)
		}
	}
	digits := digitsBuilder.String()

	if digits == "" {
		return variants
	}

	// Add version with + prefix
	if !strings.HasPrefix(phone, "+") {
		variants = append(variants, "+"+digits)
	}

	// Handle US phone numbers
	if len(digits) == 10 {
		variants = append(variants, "+1"+digits)
		variants = append(variants, "1"+digits)
	} else if len(digits) == 11 && strings.HasPrefix(digits, "1") {
		variants = append(variants, digits[1:])
		variants = append(variants, "+"+digits)
	}

	return variants
}

// loadContacts loads contacts from all AddressBook databases.
func (cr *ContactResolver) loadContacts() {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.loaded {
		return
	}
	cr.loaded = true

	dbPaths := getAddressBookPaths()
	for _, dbPath := range dbPaths {
		cr.loadFromDatabase(dbPath)
	}
}

// loadFromDatabase loads contacts from a single AddressBook database.
func (cr *ContactResolver) loadFromDatabase(dbPath string) {
	connStr := "file:" + dbPath + "?mode=ro"
	db, err := sql.Open("sqlite3", connStr)
	if err != nil {
		return
	}
	defer db.Close()

	// Load phone number to name mappings
	rows, err := db.Query(`
		SELECT 
			r.ZFIRSTNAME,
			r.ZLASTNAME,
			r.ZORGANIZATION,
			p.ZFULLNUMBER
		FROM ZABCDRECORD r
		JOIN ZABCDPHONENUMBER p ON r.Z_PK = p.ZOWNER
		WHERE p.ZFULLNUMBER IS NOT NULL
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var firstName, lastName, organization, phone sql.NullString
			if err := rows.Scan(&firstName, &lastName, &organization, &phone); err != nil {
				continue
			}

			displayName := buildDisplayName(firstName.String, lastName.String, organization.String)
			if displayName == "" || !phone.Valid {
				continue
			}

			normalized := NormalizePhoneNumber(phone.String)
			if normalized != "" {
				cr.phoneToName[normalized] = displayName
				for _, variant := range GetPhoneVariants(normalized) {
					if _, exists := cr.phoneToName[variant]; !exists {
						cr.phoneToName[variant] = displayName
					}
				}
			}
		}
	}

	// Load email to name mappings
	rows, err = db.Query(`
		SELECT 
			r.ZFIRSTNAME,
			r.ZLASTNAME,
			r.ZORGANIZATION,
			e.ZADDRESS
		FROM ZABCDRECORD r
		JOIN ZABCDEMAILADDRESS e ON r.Z_PK = e.ZOWNER
		WHERE e.ZADDRESS IS NOT NULL
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var firstName, lastName, organization, email sql.NullString
			if err := rows.Scan(&firstName, &lastName, &organization, &email); err != nil {
				continue
			}

			displayName := buildDisplayName(firstName.String, lastName.String, organization.String)
			if displayName == "" || !email.Valid {
				continue
			}

			cr.emailToName[strings.ToLower(email.String)] = displayName
		}
	}
}

func buildDisplayName(firstName, lastName, organization string) string {
	var parts []string
	if firstName != "" {
		parts = append(parts, firstName)
	}
	if lastName != "" {
		parts = append(parts, lastName)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	if organization != "" {
		return organization
	}
	return ""
}

// Resolve resolves an identifier (phone/email) to a contact name.
func (cr *ContactResolver) Resolve(identifier string) string {
	if identifier == "" {
		return identifier
	}

	cr.loadContacts()

	cr.mu.RLock()
	defer cr.mu.RUnlock()

	// Check if it's an email
	if strings.Contains(identifier, "@") {
		if name, ok := cr.emailToName[strings.ToLower(identifier)]; ok {
			return name
		}
		return identifier
	}

	// Try phone number lookup
	normalized := NormalizePhoneNumber(identifier)

	// Try direct match first
	if name, ok := cr.phoneToName[normalized]; ok {
		return name
	}

	// Try variants
	for _, variant := range GetPhoneVariants(normalized) {
		if name, ok := cr.phoneToName[variant]; ok {
			return name
		}
	}

	return identifier
}

// GetContactCount returns the number of loaded contacts.
func (cr *ContactResolver) GetContactCount() int {
	cr.loadContacts()
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return len(cr.phoneToName) + len(cr.emailToName)
}
