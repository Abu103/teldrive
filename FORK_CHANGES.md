# TelDrive Fork Improvements

This document outlines the key improvements and additions made in this fork of TelDrive.

## Integrated Telegram Bot

### Core Improvements

1. **Integrated Bot Architecture**
   - Combined the standalone Telegram bot with the TelDrive web application
   - Single command operation (`./teldrive run`) for both the web interface and bot
   - Configurable through the main `config.toml` file

2. **Channel ID Handling**
   - Fixed the issue with Telegram's channel ID format by properly handling the -100 prefix
   - Reliable string-based extraction approach for channel IDs like `-1002523726746`
   - Improved comparison logic for message source verification

3. **Parent Directory Support**
   - Dynamic parent ID implementation for organizing files into specific folders
   - Configurable parent folder ID in `config.toml`
   - Files automatically appear in the correct folder in the TelDrive web interface

4. **Duplicate File Handling**
   - Automatic detection and handling of duplicate filenames
   - Appends timestamps and random UUIDs to ensure uniqueness
   - Prevents database constraint violations

5. **MIME Type Detection**
   - Automatic MIME type detection based on file extensions
   - Ensures files are properly categorized and can be previewed in the web interface
   - Fixes database constraint violations related to null MIME types

6. **User ID Assignment**
   - Fixed user ID assignment to ensure files appear in the correct user's TelDrive web interface
   - Uses user ID 7331706161 for consistent file ownership

7. **Comprehensive Logging**
   - Detailed logging for bot operations
   - Dedicated log files for easier troubleshooting
   - Better error reporting and handling

## Additional Utilities

### File Management Tools

1. **Auto-Categorizer (`autocategorize`)**
   - Organizes files into directories based on file types
   - Supports dry-run mode for testing before actual changes
   - Customizable category definitions

2. **Fixed Folder Categorizer (`fixcat`)**
   - Moves files to a specified parent folder
   - Supports targeting specific folders or the root directory
   - Includes safety features like dry-run mode

3. **File Rescue Utility (`filerescue`)**
   - Helps recover and relocate files if they're moved to the wrong location
   - Lists available folders and their IDs
   - Provides options to move files back to root or to specific folders

4. **Parent ID Updater (`updateparent`)**
   - Moves existing files from the root directory to a specified parent directory
   - Makes it easy to reorganize files in bulk

## Database Improvements

1. **SQL Query Format**
   - Updated SQL queries to use PostgreSQL-style positional parameters
   - Improved error handling for database operations

2. **Prepared Statements**
   - Disabled prepared statements to avoid conflicts with PostgreSQL
   - Fixed errors like "prepared statement already exists" or "prepared statement does not exist"

3. **Connection Handling**
   - Properly URL-encoded special characters in the Supabase connection string
   - More reliable database connections

## Configuration

The bot functionality can be configured in the `config.toml` file:

```toml
[bot]
bot-token = "YOUR_BOT_TOKEN"
channel-id = YOUR_CHANNEL_ID
parent-id = "YOUR_PARENT_FOLDER_ID"
enabled = true
```

## Usage Instructions

1. Configure your bot token, channel ID, and parent folder ID in `config.toml`
2. Run TelDrive with the integrated bot: `./teldrive run`
3. Upload files to your Telegram channel
4. Files will automatically appear in the specified folder in TelDrive
