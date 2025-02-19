import os

GH_ACCESS_TOKEN = os.getenv("GH_ACCESS_TOKEN", "ghp_BDdVw3SGMA9Wyk5bD1qV574pAMoDsX2b3sus")
GL_ACCESS_TOKEN = os.getenv("GL_ACCESS_TOKEN", "glpat-susUjzTGvn0KuplbWf2r")
if __name__ == "__main__":
    print(f"using the gh access token {GH_ACCESS_TOKEN}")
    print(f"using the gl access token {GL_ACCESS_TOKEN}")
