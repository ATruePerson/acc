import json
import os
import urllib.request
import urllib.error
import time

PROXY_URL = os.environ.get("ACC_PROXY_URL", "http://localhost:9999/v1/messages")
HOME = os.environ.get("HOME", "~")
CONFIG_PATH = os.environ.get("ACC_CONFIG_PATH", os.path.join(HOME, ".config", "acc", "config.json"))
WORKSPACE_CONFIG_PATH = os.environ.get("ACC_WORKSPACE_CONFIG_PATH", os.path.join(HOME, "acc", "config.json"))

QUESTION_PRISONERS = """A group of 100 prisoners are in a prison, each with a unique number from 1 to 100. In a room, there are 100 closed boxes, each containing a random number from 1 to 100. The numbers in the boxes are randomly shuffled. Each prisoner enters the room one by one and can open up to 50 boxes to find their own number. They cannot communicate after entering. If ALL 100 prisoners find their own number, they are all freed. If even one fails, they are all executed.

What is the optimal strategy to maximize their chances of survival, what is the exact mathematical probability of survival under this strategy, and provide a rigorous proof of the probability."""

def update_config_max_tokens(val):
    # Update active config
    with open(CONFIG_PATH, "r") as f:
        data = json.load(f)
    data["aliases"]["anthropic/claude-mythos"]["max_tokens"] = val
    with open(CONFIG_PATH, "w") as f:
        json.dump(data, f, indent=2)
    
    # Update workspace config for consistency
    with open(WORKSPACE_CONFIG_PATH, "r") as f:
        data_ws = json.load(f)
    data_ws["aliases"]["anthropic/claude-mythos"]["max_tokens"] = val
    with open(WORKSPACE_CONFIG_PATH, "w") as f:
        json.dump(data_ws, f, indent=2)

    # Let the hot reload register (usually instant, wait 500ms)
    time.sleep(0.5)

def ask_proxy(prompt):
    payload = {
        "model": "anthropic/claude-mythos",
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": 80000  # Client request max_tokens, will be overridden by route max_tokens
    }
    
    req = urllib.request.Request(
        PROXY_URL,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "anthropic-version": "2023-06-01"
        },
        method="POST"
    )
    
    try:
        with urllib.request.urlopen(req) as res:
            resp_body = res.read().decode("utf-8")
            return json.loads(resp_body)
    except urllib.error.HTTPError as e:
        print(f"HTTP Error: {e.code} - {e.read().decode('utf-8')}")
        return None
    except Exception as e:
        print(f"Error: {e}")
        return None

def run_test(question, max_tokens_val):
    print(f"\n==================================================")
    print(f"TESTING WITH MAX_TOKENS = {max_tokens_val}")
    print(f"==================================================")
    
    update_config_max_tokens(max_tokens_val)
    
    start_time = time.time()
    response = ask_proxy(question)
    elapsed = time.time() - start_time
    
    if response:
        print(f"Status: Success (took {elapsed:.2f}s)")
        if "content" in response and len(response["content"]) > 0:
            for block in response["content"]:
                if block.get("type") == "text":
                    print("\nRESPONSE CONTENT:\n" + block.get("text", ""))
        else:
            print("Response had no text content blocks.")
    else:
        print("Failed to get response.")

def main():
    print("RUNNING 100 PRISONERS MATHEMATICAL PROOF BENCHMARK...")
    run_test(QUESTION_PRISONERS, 500)   # Low limit (should get cut off)
    run_test(QUESTION_PRISONERS, 4000)  # Medium limit (might finish, but brief)
    run_test(QUESTION_PRISONERS, 80000) # Insane/Max limit (should give an epic, fully detailed math proof!)

if __name__ == "__main__":
    main()
