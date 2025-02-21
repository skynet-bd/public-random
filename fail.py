import os
GH_ACCESS_TOKEN = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdGw3SGMA9Wyk5bD1qV574pAMoDsX2b9sus")
if __name__ == "__main__":
    print(f"using the access token {GH_ACCESS_TOKEN}")
