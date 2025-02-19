import os

GH_ACCESS_TOKEN = os.getenv("GH_ACCESS_TOKEN", "")

if __name__ == "__main__":
    print(f"using the access token {GH_ACCESS_TOKEN}")
