"""Module for reading contact names from macOS AddressBook database."""

import os
import re
import sqlite3
from pathlib import Path
from typing import Dict, Optional


def get_addressbook_paths() -> list:
    """Find all AddressBook database files on the system.
    
    macOS stores contacts in separate databases for each account source
    (iCloud, Google, On My Mac, etc.) at:
    ~/Library/Application Support/AddressBook/Sources/*/AddressBook-v22.abcddb
    
    Returns:
        List of paths to AddressBook database files
    """
    base_path = Path.home() / "Library" / "Application Support" / "AddressBook" / "Sources"
    
    if not base_path.exists():
        return []
    
    db_files = []
    try:
        for source_dir in base_path.iterdir():
            if source_dir.is_dir():
                db_file = source_dir / "AddressBook-v22.abcddb"
                if db_file.exists():
                    db_files.append(db_file)
    except PermissionError:
        pass
    
    return db_files


def normalize_phone_number(phone: str) -> str:
    """Normalize a phone number to just digits for comparison.
    
    Strips all non-digit characters except leading +.
    
    Args:
        phone: Phone number in any format
        
    Returns:
        Normalized phone number (digits only, possibly with leading +)
    """
    if not phone:
        return ""
    
    phone = phone.strip()
    
    # Keep leading + if present
    has_plus = phone.startswith('+')
    
    # Extract just the digits
    digits = ''.join(c for c in phone if c.isdigit())
    
    if has_plus:
        return '+' + digits
    return digits


def get_phone_variants(phone: str) -> list:
    """Generate common variants of a phone number for matching.
    
    Args:
        phone: Normalized phone number
        
    Returns:
        List of variants to try when matching
    """
    if not phone:
        return []
    
    variants = [phone]
    digits = ''.join(c for c in phone if c.isdigit())
    
    if not digits:
        return variants
    
    # Add version with + prefix
    if not phone.startswith('+'):
        variants.append('+' + digits)
    
    # Handle US phone numbers
    if len(digits) == 10:
        # Add with +1 prefix
        variants.append('+1' + digits)
        variants.append('1' + digits)
    elif len(digits) == 11 and digits.startswith('1'):
        # Add without country code
        variants.append(digits[1:])
        variants.append('+' + digits)
    
    return variants


class ContactResolver:
    """Resolves phone numbers and email addresses to contact names."""
    
    def __init__(self):
        """Initialize the contact resolver."""
        self._phone_to_name: Dict[str, str] = {}
        self._email_to_name: Dict[str, str] = {}
        self._loaded = False
        
    def _load_contacts(self):
        """Load contacts from all AddressBook databases."""
        if self._loaded:
            return
        
        self._loaded = True
        db_paths = get_addressbook_paths()
        
        for db_path in db_paths:
            try:
                self._load_from_database(db_path)
            except Exception:
                # Skip databases that can't be read
                continue
    
    def _load_from_database(self, db_path: Path):
        """Load contacts from a single AddressBook database.
        
        Args:
            db_path: Path to the AddressBook database file
        """
        try:
            # Connect in read-only mode
            conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()
            
            # Load phone number to name mappings
            # ZABCDRECORD contains contact info, ZABCDPHONENUMBER contains phone numbers
            # They are linked by ZOWNER (phone) -> Z_PK (record)
            try:
                cursor.execute("""
                    SELECT 
                        r.ZFIRSTNAME,
                        r.ZLASTNAME,
                        r.ZORGANIZATION,
                        p.ZFULLNUMBER
                    FROM ZABCDRECORD r
                    JOIN ZABCDPHONENUMBER p ON r.Z_PK = p.ZOWNER
                    WHERE p.ZFULLNUMBER IS NOT NULL
                """)
                
                for row in cursor.fetchall():
                    first_name = row['ZFIRSTNAME'] or ''
                    last_name = row['ZLASTNAME'] or ''
                    organization = row['ZORGANIZATION'] or ''
                    phone = row['ZFULLNUMBER']
                    
                    # Build display name
                    name_parts = [p for p in [first_name, last_name] if p]
                    if name_parts:
                        display_name = ' '.join(name_parts)
                    elif organization:
                        display_name = organization
                    else:
                        continue
                    
                    # Normalize and store the phone number
                    normalized = normalize_phone_number(phone)
                    if normalized:
                        self._phone_to_name[normalized] = display_name
                        
                        # Also add variants
                        for variant in get_phone_variants(normalized):
                            if variant not in self._phone_to_name:
                                self._phone_to_name[variant] = display_name
                                
            except sqlite3.OperationalError:
                # Table might not exist in this database
                pass
            
            # Load email to name mappings
            try:
                cursor.execute("""
                    SELECT 
                        r.ZFIRSTNAME,
                        r.ZLASTNAME,
                        r.ZORGANIZATION,
                        e.ZADDRESS
                    FROM ZABCDRECORD r
                    JOIN ZABCDEMAILADDRESS e ON r.Z_PK = e.ZOWNER
                    WHERE e.ZADDRESS IS NOT NULL
                """)
                
                for row in cursor.fetchall():
                    first_name = row['ZFIRSTNAME'] or ''
                    last_name = row['ZLASTNAME'] or ''
                    organization = row['ZORGANIZATION'] or ''
                    email = row['ZADDRESS']
                    
                    # Build display name
                    name_parts = [p for p in [first_name, last_name] if p]
                    if name_parts:
                        display_name = ' '.join(name_parts)
                    elif organization:
                        display_name = organization
                    else:
                        continue
                    
                    # Normalize and store the email
                    if email:
                        self._email_to_name[email.lower()] = display_name
                        
            except sqlite3.OperationalError:
                # Table might not exist in this database
                pass
            
            conn.close()
            
        except (sqlite3.Error, PermissionError):
            # Can't access database
            pass
    
    def resolve(self, identifier: str) -> str:
        """Resolve an identifier (phone/email) to a contact name.
        
        Args:
            identifier: Phone number or email address
            
        Returns:
            Contact name if found, otherwise the original identifier
        """
        if not identifier:
            return identifier
        
        # Load contacts on first use
        self._load_contacts()
        
        # Check if it's an email
        if '@' in identifier:
            name = self._email_to_name.get(identifier.lower())
            if name:
                return name
            return identifier
        
        # Try phone number lookup
        normalized = normalize_phone_number(identifier)
        
        # Try direct match first
        if normalized in self._phone_to_name:
            return self._phone_to_name[normalized]
        
        # Try variants
        for variant in get_phone_variants(normalized):
            if variant in self._phone_to_name:
                return self._phone_to_name[variant]
        
        # Not found, return original
        return identifier
    
    def get_contact_count(self) -> int:
        """Get the number of loaded contacts.
        
        Returns:
            Number of unique phone/email to name mappings
        """
        self._load_contacts()
        return len(self._phone_to_name) + len(self._email_to_name)


# Global resolver instance (lazy loaded)
_resolver: Optional[ContactResolver] = None


def get_contact_name(identifier: str) -> str:
    """Get the contact name for a phone number or email.
    
    This is a convenience function that uses a global ContactResolver.
    
    Args:
        identifier: Phone number or email address
        
    Returns:
        Contact name if found, otherwise the original identifier
    """
    global _resolver
    if _resolver is None:
        _resolver = ContactResolver()
    return _resolver.resolve(identifier)


def preload_contacts():
    """Preload contacts into memory.
    
    Call this early to avoid delays when first resolving a contact.
    """
    global _resolver
    if _resolver is None:
        _resolver = ContactResolver()
    _resolver._load_contacts()
