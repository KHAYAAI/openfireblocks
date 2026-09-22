"""Setup configuration for OpenFireblocks Python SDK."""

from setuptools import setup, find_packages

setup(
    name="openfireblocks",
    version="1.0.0",
    description="Enterprise cryptocurrency key management and signing platform SDK",
    author="OpenFireblocks",
    author_email="support@openfireblocks.io",
    url="https://github.com/openfireblocks/sdk-python",
    packages=find_packages(),
    python_requires=">=3.8",
    classifiers=[
        "Development Status :: 5 - Production/Stable",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: Apache Software License",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
        "Topic :: Software Development :: Libraries",
        "Topic :: Office/Business :: Financial",
    ],
)
