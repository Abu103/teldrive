# Repository Cleanup Script
# This script removes unnecessary files before uploading to GitHub

# Log files
Write-Host "Removing log files..." -ForegroundColor Green
Remove-Item -Path "bottest.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "fixedbot.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "integrated_bot.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "output.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "simplebot.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "teldrive_standalone_bot.log" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "teldrive_bot_log.txt" -Force -ErrorAction SilentlyContinue

# Test directories
Write-Host "Removing test directories..." -ForegroundColor Green
Remove-Item -Path "bottest" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\bottest" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\channeltest" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dbtest" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\parenttest" -Recurse -Force -ErrorAction SilentlyContinue

# Redundant bot implementations
Write-Host "Removing redundant bot implementations..." -ForegroundColor Green
Remove-Item -Path "cmd\botfix" -Recurse -Force -ErrorAction SilentlyContinue

# Temporary database tools
Write-Host "Removing temporary database tools..." -ForegroundColor Green
Remove-Item -Path "cmd\dbcleanup" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dblist" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dbparentcheck" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dbquery" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dbrootfix" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\dbschema" -Recurse -Force -ErrorAction SilentlyContinue

# Duplicate utilities
Write-Host "Removing duplicate utilities..." -ForegroundColor Green
Remove-Item -Path "cmd\checkparent" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path "cmd\createfolder" -Recurse -Force -ErrorAction SilentlyContinue

# Backup directories (comment these out if you want to keep backups locally)
# Write-Host "Removing backup directories..." -ForegroundColor Green
# Remove-Item -Path "backup_2025_05_10" -Recurse -Force -ErrorAction SilentlyContinue
# Remove-Item -Path "backup_2025_05_10_final" -Recurse -Force -ErrorAction SilentlyContinue

# Cleanup the cleanup files themselves
Write-Host "Removing cleanup files..." -ForegroundColor Green
Remove-Item -Path "cleanup_list" -Recurse -Force -ErrorAction SilentlyContinue

Write-Host "Cleanup complete!" -ForegroundColor Green
Write-Host "Your repository is now ready for GitHub." -ForegroundColor Green
