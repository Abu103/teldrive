# TelDrive with Integrated Telegram Bot

This enhanced fork of TelDrive includes a fully integrated Telegram bot that automatically uploads files from your Telegram channel directly to your TelDrive storage with proper folder organization. TelDrive is a powerful utility that enables you to organize your Telegram files and much more.

## Advantages Over Alternative Solutions

- **Exceptional Speed:** Teldrive stands out among similar tools, thanks to its implementation in Go, a language known for its efficiency. Its performance surpasses alternatives written in Python and other languages, with the exception of Rust.

- **Enhanced Management Capabilities:** Teldrive not only excels in speed but also offers an intuitive user interface for efficient file interaction which other tool lacks. Its compatibility with Rclone further enhances file management.

> [!IMPORTANT]
> Teldrive functions as a wrapper over your Telegram account, simplifying file access. However, users must adhere to the limitations imposed by the Telegram API. Teldrive is not responsible for any consequences arising from non-compliance with these API limits.You will be banned instantly if you misuse telegram API.

Visit https://teldrive-docs.pages.dev for setting up teldrive.

## Integrated Telegram Bot Features

This enhanced fork adds several powerful features to TelDrive:

### 1. Single Command Operation

Run both TelDrive and the Telegram bot with a single command:
```
./teldrive run
```
No need to run separate processes for the web interface and the bot.

### 2. Automatic File Organization

Files uploaded to your Telegram channel are automatically added to your specified parent folder in TelDrive. Configure the parent folder ID in the `config.toml` file.

### 3. Channel ID Handling

The bot correctly handles Telegram's channel ID format with the -100 prefix, ensuring proper message processing from public and private channels.

### 4. Duplicate File Handling

The bot automatically handles duplicate filenames by appending timestamps and random UUIDs, preventing database constraint violations.

### 5. File Categorization Tools

This fork includes additional utilities for file management:

- **autocategorize**: Organizes files into directories based on file types
- **fixcat**: Moves files to a specified parent folder
- **filerescue**: Helps recover and relocate files if they're moved to the wrong location

### Configuration

Edit the `config.toml` file to customize the bot behavior:

```toml
[bot]
bot-token = "YOUR_BOT_TOKEN"
channel-id = YOUR_CHANNEL_ID
parent-id = "YOUR_PARENT_FOLDER_ID"
enabled = true
```

# Recognitions

<a href="https://trendshift.io/repositories/7568" target="_blank"><img src="https://trendshift.io/api/badge/repositories/7568" alt="divyam234%2Fteldrive | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>

## Best Practices for Using Teldrive

### Dos:

- **Follow Limits:** Adhere to the limits imposed by Telegram servers to avoid account bans and automatic deletion of your channel.Your files will be removed from telegram servers if you try to abuse the service as most people have zero brains they will still do so good luck.
- **Responsible Storage:** Be mindful of the content you store on Telegram. Utilize storage efficiently and only keep data that serves a purpose.
  
### Don'ts:
- **Data Hoarding:** Avoid excessive data hoarding, as it not only violates Telegram's terms.
  
By following these guidelines, you contribute to the responsible and effective use of Telegram, maintaining a fair and equitable environment for all users.

## Contributing

Feel free to contribute to this project.See [CONTRIBUTING.md](CONTRIBUTING.md) for more information.

## Donate

If you like this project small contribution would be appreciated [Paypal](https://paypal.me/redux234).

## Star History

<a href="https://www.star-history.com/#tgdrive/teldrive&Date">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=tgdrive/teldrive&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=tgdrive/teldrive&type=Date" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=tgdrive/teldrive&type=Date" />
 </picture>
</a>
