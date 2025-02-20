import os

GH_ACCESS_TOKEN1 = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdGw3SGMA9Wyk5bD1qV574pAMoDsX2b1sus")
GH_ACCESS_TOKEN2 = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdGw3SGMA9Wyk5bD1qV574pAMoDsX2b2sus")
GH_ACCESS_TOKEN3 = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdGw3SGMA9Wyk5bD1qV574pAMoDsX2b3sus")
GH_ACCESS_TOKEN4 = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdGw3SGMA9Wyk5bD1qV574pAMoDsX2b4sus")

if __name__ == "__main__":
    print(f"using the access token {GH_ACCESS_TOKEN1}")
