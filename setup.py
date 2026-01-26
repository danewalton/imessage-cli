"""Setup script for iMessage CLI."""

from setuptools import setup, find_packages

with open("README.md", "r", encoding="utf-8") as fh:
    long_description = fh.read()

setup(
    name="imessage-cli",
    version="1.0.0",
    author="Your Name",
    author_email="your.email@example.com",
    description="Read and respond to iMessages from the command line",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/yourusername/imessage-cli",
    packages=find_packages(),
    classifiers=[
        "Development Status :: 4 - Beta",
        "Environment :: Console",
        "Intended Audience :: End Users/Desktop",
        "License :: OSI Approved :: MIT License",
        "Operating System :: MacOS :: MacOS X",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
        "Topic :: Communications :: Chat",
    ],
    python_requires=">=3.8",
    entry_points={
        "console_scripts": [
            "imessage=imessage_cli.cli:main",
        ],
    },
    install_requires=[
        # No external dependencies - uses only Python standard library
    ],
)
