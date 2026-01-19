# MCP Tools Reference

## Available Tools

### example_tool

An example tool that echoes a message.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `message` | string | Yes | The message to echo |

**Example:**

```json
{
  "tool": "example_tool",
  "arguments": {
    "message": "Hello, world!"
  }
}
```

**Response:**

```
Echo: Hello, world!
```
