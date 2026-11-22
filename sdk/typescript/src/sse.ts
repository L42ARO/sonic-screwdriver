/** Server-Sent Events: read a streaming response as (event name, JSON data) pairs. */
export async function* events(body: ReadableStream<Uint8Array>): AsyncGenerator<[string, any]> {
  const decoder = new TextDecoder();
  let buffer = "";
  let name = "message";
  let data: string[] = [];
  for await (const chunk of body) {
    buffer += decoder.decode(chunk, { stream: true });
    let newline: number;
    while ((newline = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      buffer = buffer.slice(newline + 1);
      if (line.startsWith("event:")) name = line.slice(6).trim();
      else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
      else if (line === "" && data.length) {
        yield [name, JSON.parse(data.join("\n"))];
        name = "message";
        data = [];
      }
    }
  }
}
