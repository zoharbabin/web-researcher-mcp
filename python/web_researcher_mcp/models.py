"""
web_researcher_mcp.models
~~~~~~~~~~~~~~~~~~~~~~~~~
Dataclasses for web-researcher-mcp tool responses.

AUTO-GENERATED — do not edit by hand.
Run: make gen-python-client
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


class MCPError(Exception):
    """Raised when a tool call returns isError: true or a JSON-RPC error."""

    def __init__(self, message: str, code: str | None = None) -> None:
        super().__init__(message)
        self.message = message
        self.code = code


@dataclass
class AcademicSearchPaper:
    abstract: Optional[str] = None
    authors: list[str] = field(default_factory=list)
    citationCount: Optional[int] = None
    citationIntents: list[str] = field(default_factory=list)
    doi: Optional[str] = None
    isInfluential: Optional[bool] = None
    journal: Optional[str] = None
    openAccess: Optional[bool] = None
    pdfUrl: Optional[str] = None
    source: Optional[str] = None
    title: Optional[str] = None
    tldr: Optional[str] = None
    url: Optional[str] = None
    year: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AcademicSearchPaper | None":
        if d is None:
            return None
        return cls(
            abstract=d.get('abstract'),
            authors=list(d.get('authors') or []),
            citationCount=d.get('citationCount'),
            citationIntents=list(d.get('citationIntents') or []),
            doi=d.get('doi'),
            isInfluential=d.get('isInfluential'),
            journal=d.get('journal'),
            openAccess=d.get('openAccess'),
            pdfUrl=d.get('pdfUrl'),
            source=d.get('source'),
            title=d.get('title'),
            tldr=d.get('tldr'),
            url=d.get('url'),
            year=d.get('year'),
        )

@dataclass
class AcademicSearchResponse:
    hints: dict[str, Any] = field(default_factory=dict)
    papers: list[AcademicSearchPaper] = field(default_factory=list)
    query: Optional[str] = None
    resultCount: Optional[int] = None
    source: Optional[str] = None
    totalResults: Optional[int] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AcademicSearchResponse | None":
        if d is None:
            return None
        return cls(
            hints=dict(d.get('hints') or {}),
            papers=[AcademicSearchPaper.from_dict(i) for i in (d.get('papers') or [])],
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            source=d.get('source'),
            totalResults=d.get('totalResults'),
            trust=d.get('trust'),
        )

@dataclass
class Analytics:
    firstSeen: Optional[str] = None
    lastSeen: Optional[str] = None
    recentTools: list[str] = field(default_factory=list)
    tenantId: Optional[str] = None
    toolCounts: dict[str, Any] = field(default_factory=dict)
    totalCalls: Optional[int] = None
    userId: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Analytics | None":
        if d is None:
            return None
        return cls(
            firstSeen=d.get('firstSeen'),
            lastSeen=d.get('lastSeen'),
            recentTools=list(d.get('recentTools') or []),
            tenantId=d.get('tenantId'),
            toolCounts=dict(d.get('toolCounts') or {}),
            totalCalls=d.get('totalCalls'),
            userId=d.get('userId'),
        )

@dataclass
class AnswerCitation:
    publishedDate: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AnswerCitation | None":
        if d is None:
            return None
        return cls(
            publishedDate=d.get('publishedDate'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class AnswerResponse:
    answer: Optional[str] = None
    citations: list[AnswerCitation] = field(default_factory=list)
    costUsd: Optional[float] = None
    hints: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AnswerResponse | None":
        if d is None:
            return None
        return cls(
            answer=d.get('answer'),
            citations=[AnswerCitation.from_dict(i) for i in (d.get('citations') or [])],
            costUsd=d.get('costUsd'),
            hints=dict(d.get('hints') or {}),
            provider=d.get('provider'),
            trust=d.get('trust'),
        )

@dataclass
class ArchiveSourceResponse:
    archivedAt: Optional[str] = None
    captured: Optional[bool] = None
    httpStatus: Optional[int] = None
    pollUrl: Optional[str] = None
    provenance: list[str] = field(default_factory=list)
    reason: Optional[str] = None
    requestedUrl: Optional[str] = None
    snapshotUrl: Optional[str] = None
    source: Optional[str] = None
    status: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ArchiveSourceResponse | None":
        if d is None:
            return None
        return cls(
            archivedAt=d.get('archivedAt'),
            captured=d.get('captured'),
            httpStatus=d.get('httpStatus'),
            pollUrl=d.get('pollUrl'),
            provenance=list(d.get('provenance') or []),
            reason=d.get('reason'),
            requestedUrl=d.get('requestedUrl'),
            snapshotUrl=d.get('snapshotUrl'),
            source=d.get('source'),
            status=d.get('status'),
            trust=d.get('trust'),
        )

@dataclass
class AuditBibliographyEntry:
    archivedUrl: Optional[str] = None
    claim: Optional[str] = None
    claimEvidence: list[str] = field(default_factory=list)
    claimSourceUrl: Optional[str] = None
    claimSupport: Optional[str] = None
    contrastSignal: Optional[bool] = None
    doi: Optional[str] = None
    exists: Optional[bool] = None
    flags: list[str] = field(default_factory=list)
    httpStatus: Optional[int] = None
    index: Optional[int] = None
    linkLive: Optional[bool] = None
    reason: Optional[str] = None
    retractionStatus: dict[str, Any] = field(default_factory=dict)
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AuditBibliographyEntry | None":
        if d is None:
            return None
        return cls(
            archivedUrl=d.get('archivedUrl'),
            claim=d.get('claim'),
            claimEvidence=list(d.get('claimEvidence') or []),
            claimSourceUrl=d.get('claimSourceUrl'),
            claimSupport=d.get('claimSupport'),
            contrastSignal=d.get('contrastSignal'),
            doi=d.get('doi'),
            exists=d.get('exists'),
            flags=list(d.get('flags') or []),
            httpStatus=d.get('httpStatus'),
            index=d.get('index'),
            linkLive=d.get('linkLive'),
            reason=d.get('reason'),
            retractionStatus=dict(d.get('retractionStatus') or {}),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class AuditBibliographyResponse:
    checkedAt: Optional[str] = None
    entries: list[AuditBibliographyEntry] = field(default_factory=list)
    entryCount: Optional[int] = None
    skipped: Optional[int] = None
    skippedNote: Optional[str] = None
    source: Optional[str] = None
    summary: Optional[AuditBibliographySummary] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "AuditBibliographyResponse | None":
        if d is None:
            return None
        return cls(
            checkedAt=d.get('checkedAt'),
            entries=[AuditBibliographyEntry.from_dict(i) for i in (d.get('entries') or [])],
            entryCount=d.get('entryCount'),
            skipped=d.get('skipped'),
            skippedNote=d.get('skippedNote'),
            source=d.get('source'),
            summary=AuditBibliographySummary.from_dict(d.get('summary')) if d.get('summary') else AuditBibliographySummary(),
            trust=d.get('trust'),
        )

@dataclass
class Citation:
    accessedDate: Optional[str] = None
    formatted: Optional[Formatted] = None
    metadata: Optional[Metadata] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Citation | None":
        if d is None:
            return None
        return cls(
            accessedDate=d.get('accessedDate'),
            formatted=Formatted.from_dict(d.get('formatted')) if d.get('formatted') else None,
            metadata=Metadata.from_dict(d.get('metadata')) if d.get('metadata') else None,
            url=d.get('url'),
        )

@dataclass
class CitationGraphCitedby:
    abstract: Optional[str] = None
    authors: list[str] = field(default_factory=list)
    citationCount: Optional[int] = None
    citationIntents: list[str] = field(default_factory=list)
    doi: Optional[str] = None
    isInfluential: Optional[bool] = None
    journal: Optional[str] = None
    openAccess: Optional[bool] = None
    pdfUrl: Optional[str] = None
    source: Optional[str] = None
    title: Optional[str] = None
    tldr: Optional[str] = None
    url: Optional[str] = None
    year: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "CitationGraphCitedby | None":
        if d is None:
            return None
        return cls(
            abstract=d.get('abstract'),
            authors=list(d.get('authors') or []),
            citationCount=d.get('citationCount'),
            citationIntents=list(d.get('citationIntents') or []),
            doi=d.get('doi'),
            isInfluential=d.get('isInfluential'),
            journal=d.get('journal'),
            openAccess=d.get('openAccess'),
            pdfUrl=d.get('pdfUrl'),
            source=d.get('source'),
            title=d.get('title'),
            tldr=d.get('tldr'),
            url=d.get('url'),
            year=d.get('year'),
        )

@dataclass
class CitationGraphResponse:
    citedBy: list[CitationGraphCitedby] = field(default_factory=list)
    citedByCount: Optional[int] = None
    direction: Optional[str] = None
    provider: Optional[str] = None
    references: list[CitationGraphCitedby] = field(default_factory=list)
    referencesCount: Optional[int] = None
    seed: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "CitationGraphResponse | None":
        if d is None:
            return None
        return cls(
            citedBy=[CitationGraphCitedby.from_dict(i) for i in (d.get('citedBy') or [])],
            citedByCount=d.get('citedByCount'),
            direction=d.get('direction'),
            provider=d.get('provider'),
            references=[CitationGraphCitedby.from_dict(i) for i in (d.get('references') or [])],
            referencesCount=d.get('referencesCount'),
            seed=d.get('seed'),
            trust=d.get('trust'),
        )

@dataclass
class ClinicalSearchResponse:
    hints: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    trials: list[ClinicalSearchTrial] = field(default_factory=list)
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ClinicalSearchResponse | None":
        if d is None:
            return None
        return cls(
            hints=dict(d.get('hints') or {}),
            provider=d.get('provider'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            trials=[ClinicalSearchTrial.from_dict(i) for i in (d.get('trials') or [])],
            trust=d.get('trust'),
        )

@dataclass
class ClinicalSearchTrial:
    conditions: list[str] = field(default_factory=list)
    hasResults: Optional[bool] = None
    interventions: list[str] = field(default_factory=list)
    nctId: Optional[str] = None
    phases: list[str] = field(default_factory=list)
    source: Optional[str] = None
    sponsor: Optional[str] = None
    startDate: Optional[str] = None
    status: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ClinicalSearchTrial | None":
        if d is None:
            return None
        return cls(
            conditions=list(d.get('conditions') or []),
            hasResults=d.get('hasResults'),
            interventions=list(d.get('interventions') or []),
            nctId=d.get('nctId'),
            phases=list(d.get('phases') or []),
            source=d.get('source'),
            sponsor=d.get('sponsor'),
            startDate=d.get('startDate'),
            status=d.get('status'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class ConflictOfInterest:
    authorAffiliation: Optional[str] = None
    citedEntityName: Optional[str] = None
    confidence: Optional[str] = None
    conflictType: Optional[str] = None
    detected: Optional[bool] = None
    evidence: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ConflictOfInterest | None":
        if d is None:
            return None
        return cls(
            authorAffiliation=d.get('authorAffiliation'),
            citedEntityName=d.get('citedEntityName'),
            confidence=d.get('confidence'),
            conflictType=d.get('conflictType'),
            detected=d.get('detected'),
            evidence=d.get('evidence'),
        )

@dataclass
class Coverage:
    domainSpread: Optional[float] = None
    dominantDomain: Optional[str] = None
    gaps: list[str] = field(default_factory=list)
    sourceCount: Optional[int] = None
    sourceTypes: dict[str, Any] = field(default_factory=dict)
    uniqueDomains: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Coverage | None":
        if d is None:
            return None
        return cls(
            domainSpread=d.get('domainSpread'),
            dominantDomain=d.get('dominantDomain'),
            gaps=list(d.get('gaps') or []),
            sourceCount=d.get('sourceCount'),
            sourceTypes=dict(d.get('sourceTypes') or {}),
            uniqueDomains=d.get('uniqueDomains'),
        )

@dataclass
class EconSearchResponse:
    country: Optional[str] = None
    hints: dict[str, Any] = field(default_factory=dict)
    mode: Optional[str] = None
    provider: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    results: list[EconSearchResult] = field(default_factory=list)
    seriesId: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "EconSearchResponse | None":
        if d is None:
            return None
        return cls(
            country=d.get('country'),
            hints=dict(d.get('hints') or {}),
            mode=d.get('mode'),
            provider=d.get('provider'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            results=[EconSearchResult.from_dict(i) for i in (d.get('results') or [])],
            seriesId=d.get('seriesId'),
            trust=d.get('trust'),
        )

@dataclass
class EconSearchResult:
    date: Optional[str] = None
    frequency: Optional[str] = None
    lastUpdated: Optional[str] = None
    notes: Optional[str] = None
    seriesId: Optional[str] = None
    source: Optional[str] = None
    title: Optional[str] = None
    units: Optional[str] = None
    value: Optional[float] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "EconSearchResult | None":
        if d is None:
            return None
        return cls(
            date=d.get('date'),
            frequency=d.get('frequency'),
            lastUpdated=d.get('lastUpdated'),
            notes=d.get('notes'),
            seriesId=d.get('seriesId'),
            source=d.get('source'),
            title=d.get('title'),
            units=d.get('units'),
            value=d.get('value'),
        )

@dataclass
class FilingSearchFiling:
    accession: Optional[str] = None
    cik: Optional[str] = None
    company: Optional[str] = None
    concept: Optional[str] = None
    description: Optional[str] = None
    filingDate: Optional[str] = None
    formType: Optional[str] = None
    periodOfReport: Optional[str] = None
    source: Optional[str] = None
    unit: Optional[str] = None
    url: Optional[str] = None
    value: Optional[float] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "FilingSearchFiling | None":
        if d is None:
            return None
        return cls(
            accession=d.get('accession'),
            cik=d.get('cik'),
            company=d.get('company'),
            concept=d.get('concept'),
            description=d.get('description'),
            filingDate=d.get('filingDate'),
            formType=d.get('formType'),
            periodOfReport=d.get('periodOfReport'),
            source=d.get('source'),
            unit=d.get('unit'),
            url=d.get('url'),
            value=d.get('value'),
        )

@dataclass
class FilingSearchResponse:
    filings: list[FilingSearchFiling] = field(default_factory=list)
    hints: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "FilingSearchResponse | None":
        if d is None:
            return None
        return cls(
            filings=[FilingSearchFiling.from_dict(i) for i in (d.get('filings') or [])],
            hints=dict(d.get('hints') or {}),
            provider=d.get('provider'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            trust=d.get('trust'),
        )

@dataclass
class FormatBibliographyResponse:
    bibliography: Optional[str] = None
    entryCount: Optional[int] = None
    sessionId: Optional[str] = None
    style: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "FormatBibliographyResponse | None":
        if d is None:
            return None
        return cls(
            bibliography=d.get('bibliography'),
            entryCount=d.get('entryCount'),
            sessionId=d.get('sessionId'),
            style=d.get('style'),
            trust=d.get('trust'),
        )

@dataclass
class Formatted:
    apa: Optional[str] = None
    bibtex: Optional[str] = None
    mla: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Formatted | None":
        if d is None:
            return None
        return cls(
            apa=d.get('apa'),
            bibtex=d.get('bibtex'),
            mla=d.get('mla'),
        )

@dataclass
class GetMyAnalyticsResponse:
    analytics: Optional[Analytics] = None
    reason: Optional[str] = None
    status: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetMyAnalyticsResponse | None":
        if d is None:
            return None
        return cls(
            analytics=Analytics.from_dict(d.get('analytics')) if d.get('analytics') else None,
            reason=d.get('reason'),
            status=d.get('status'),
        )

@dataclass
class GetResearchSessionErrorpattern:
    affectedUrls: list[str] = field(default_factory=list)
    count: Optional[int] = None
    kind: Optional[str] = None
    lastSeen: Optional[str] = None
    suggestion: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionErrorpattern | None":
        if d is None:
            return None
        return cls(
            affectedUrls=list(d.get('affectedUrls') or []),
            count=d.get('count'),
            kind=d.get('kind'),
            lastSeen=d.get('lastSeen'),
            suggestion=d.get('suggestion'),
        )

@dataclass
class GetResearchSessionGap:
    description: Optional[str] = None
    foundInStep: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionGap | None":
        if d is None:
            return None
        return cls(
            description=d.get('description'),
            foundInStep=d.get('foundInStep'),
        )

@dataclass
class GetResearchSessionLaststep:
    branchId: Optional[str] = None
    confidence: Optional[str] = None
    description: Optional[str] = None
    isRevision: Optional[bool] = None
    reasoning: Optional[str] = None
    rejectedApproaches: list[str] = field(default_factory=list)
    revisesStep: Optional[int] = None
    stepNumber: Optional[int] = None
    timestamp: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionLaststep | None":
        if d is None:
            return None
        return cls(
            branchId=d.get('branchId'),
            confidence=d.get('confidence'),
            description=d.get('description'),
            isRevision=d.get('isRevision'),
            reasoning=d.get('reasoning'),
            rejectedApproaches=list(d.get('rejectedApproaches') or []),
            revisesStep=d.get('revisesStep'),
            stepNumber=d.get('stepNumber'),
            timestamp=d.get('timestamp'),
        )

@dataclass
class GetResearchSessionResponse:
    errorPatterns: list[GetResearchSessionErrorpattern] = field(default_factory=list)
    gaps: list[GetResearchSessionGap] = field(default_factory=list)
    lastSteps: list[GetResearchSessionLaststep] = field(default_factory=list)
    providerStats: dict[str, Any] = field(default_factory=dict)
    researchGoal: Optional[str] = None
    responseMode: Optional[str] = None
    sessionId: Optional[str] = None
    sources: list[GetResearchSessionSource] = field(default_factory=list)
    startedAt: Optional[str] = None
    step: Optional[GetResearchSessionLaststep] = None
    stepCount: Optional[int] = None
    stepIndex: list[GetResearchSessionStepindex] = field(default_factory=list)
    summary: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionResponse | None":
        if d is None:
            return None
        return cls(
            errorPatterns=[GetResearchSessionErrorpattern.from_dict(i) for i in (d.get('errorPatterns') or [])],
            gaps=[GetResearchSessionGap.from_dict(i) for i in (d.get('gaps') or [])],
            lastSteps=[GetResearchSessionLaststep.from_dict(i) for i in (d.get('lastSteps') or [])],
            providerStats=dict(d.get('providerStats') or {}),
            researchGoal=d.get('researchGoal'),
            responseMode=d.get('responseMode'),
            sessionId=d.get('sessionId'),
            sources=[GetResearchSessionSource.from_dict(i) for i in (d.get('sources') or [])],
            startedAt=d.get('startedAt'),
            step=GetResearchSessionLaststep.from_dict(d.get('step')) if d.get('step') else None,
            stepCount=d.get('stepCount'),
            stepIndex=[GetResearchSessionStepindex.from_dict(i) for i in (d.get('stepIndex') or [])],
            summary=d.get('summary'),
            trust=d.get('trust'),
        )

@dataclass
class GetResearchSessionSource:
    foundInStep: Optional[int] = None
    relevance: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionSource | None":
        if d is None:
            return None
        return cls(
            foundInStep=d.get('foundInStep'),
            relevance=d.get('relevance'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class GetResearchSessionStepindex:
    branchId: Optional[str] = None
    confidence: Optional[str] = None
    oneLiner: Optional[str] = None
    stepNumber: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "GetResearchSessionStepindex | None":
        if d is None:
            return None
        return cls(
            branchId=d.get('branchId'),
            confidence=d.get('confidence'),
            oneLiner=d.get('oneLiner'),
            stepNumber=d.get('stepNumber'),
        )

@dataclass
class ImageSearchImage:
    contextLink: Optional[str] = None
    displayLink: Optional[str] = None
    fileSize: Optional[str] = None
    height: Optional[int] = None
    link: Optional[str] = None
    thumbnailLink: Optional[str] = None
    title: Optional[str] = None
    width: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ImageSearchImage | None":
        if d is None:
            return None
        return cls(
            contextLink=d.get('contextLink'),
            displayLink=d.get('displayLink'),
            fileSize=d.get('fileSize'),
            height=d.get('height'),
            link=d.get('link'),
            thumbnailLink=d.get('thumbnailLink'),
            title=d.get('title'),
            width=d.get('width'),
        )

@dataclass
class ImageSearchResponse:
    images: list[ImageSearchImage] = field(default_factory=list)
    query: Optional[str] = None
    resultCount: Optional[int] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ImageSearchResponse | None":
        if d is None:
            return None
        return cls(
            images=[ImageSearchImage.from_dict(i) for i in (d.get('images') or [])],
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            trust=d.get('trust'),
        )

@dataclass
class LegalSearchCas:
    caseName: Optional[str] = None
    citation: Optional[str] = None
    citationCount: Optional[int] = None
    court: Optional[str] = None
    courtId: Optional[str] = None
    dateFiled: Optional[str] = None
    docketNumber: Optional[str] = None
    source: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "LegalSearchCas | None":
        if d is None:
            return None
        return cls(
            caseName=d.get('caseName'),
            citation=d.get('citation'),
            citationCount=d.get('citationCount'),
            court=d.get('court'),
            courtId=d.get('courtId'),
            dateFiled=d.get('dateFiled'),
            docketNumber=d.get('docketNumber'),
            source=d.get('source'),
            url=d.get('url'),
        )

@dataclass
class LegalSearchResponse:
    cases: list[LegalSearchCas] = field(default_factory=list)
    hints: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "LegalSearchResponse | None":
        if d is None:
            return None
        return cls(
            cases=[LegalSearchCas.from_dict(i) for i in (d.get('cases') or [])],
            hints=dict(d.get('hints') or {}),
            provider=d.get('provider'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            trust=d.get('trust'),
        )

@dataclass
class MemoryRecallMemory:
    createdAt: Optional[str] = None
    id: Optional[str] = None
    note: Optional[str] = None
    tags: list[str] = field(default_factory=list)
    tenantId: Optional[str] = None
    topic: Optional[str] = None
    url: Optional[str] = None
    userId: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "MemoryRecallMemory | None":
        if d is None:
            return None
        return cls(
            createdAt=d.get('createdAt'),
            id=d.get('id'),
            note=d.get('note'),
            tags=list(d.get('tags') or []),
            tenantId=d.get('tenantId'),
            topic=d.get('topic'),
            url=d.get('url'),
            userId=d.get('userId'),
        )

@dataclass
class MemoryRecallResponse:
    count: Optional[int] = None
    memories: list[MemoryRecallMemory] = field(default_factory=list)
    reason: Optional[str] = None
    status: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "MemoryRecallResponse | None":
        if d is None:
            return None
        return cls(
            count=d.get('count'),
            memories=[MemoryRecallMemory.from_dict(i) for i in (d.get('memories') or [])],
            reason=d.get('reason'),
            status=d.get('status'),
            trust=d.get('trust'),
        )

@dataclass
class MemorySaveResponse:
    createdAt: Optional[str] = None
    id: Optional[str] = None
    reason: Optional[str] = None
    status: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "MemorySaveResponse | None":
        if d is None:
            return None
        return cls(
            createdAt=d.get('createdAt'),
            id=d.get('id'),
            reason=d.get('reason'),
            status=d.get('status'),
        )

@dataclass
class Metadata:
    author: Optional[str] = None
    date: Optional[str] = None
    site: Optional[str] = None
    title: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Metadata | None":
        if d is None:
            return None
        return cls(
            author=d.get('author'),
            date=d.get('date'),
            site=d.get('site'),
            title=d.get('title'),
        )

@dataclass
class NewsSearchArticle:
    publishedAt: Optional[str] = None
    snippet: Optional[str] = None
    source: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "NewsSearchArticle | None":
        if d is None:
            return None
        return cls(
            publishedAt=d.get('publishedAt'),
            snippet=d.get('snippet'),
            source=d.get('source'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class NewsSearchResponse:
    articles: list[NewsSearchArticle] = field(default_factory=list)
    hints: dict[str, Any] = field(default_factory=dict)
    query: Optional[str] = None
    resultCount: Optional[int] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "NewsSearchResponse | None":
        if d is None:
            return None
        return cls(
            articles=[NewsSearchArticle.from_dict(i) for i in (d.get('articles') or [])],
            hints=dict(d.get('hints') or {}),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            trust=d.get('trust'),
        )

@dataclass
class PatentSearchPatent:
    abstract: Optional[str] = None
    assignee: Optional[str] = None
    filed: Optional[str] = None
    granted: Optional[str] = None
    inventor: Optional[str] = None
    number: Optional[str] = None
    pdf: Optional[str] = None
    status: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "PatentSearchPatent | None":
        if d is None:
            return None
        return cls(
            abstract=d.get('abstract'),
            assignee=d.get('assignee'),
            filed=d.get('filed'),
            granted=d.get('granted'),
            inventor=d.get('inventor'),
            number=d.get('number'),
            pdf=d.get('pdf'),
            status=d.get('status'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class PatentSearchResponse:
    hints: dict[str, Any] = field(default_factory=dict)
    patents: list[PatentSearchPatent] = field(default_factory=list)
    query: Optional[str] = None
    resultCount: Optional[int] = None
    searchType: Optional[str] = None
    searchUrl: Optional[str] = None
    source: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "PatentSearchResponse | None":
        if d is None:
            return None
        return cls(
            hints=dict(d.get('hints') or {}),
            patents=[PatentSearchPatent.from_dict(i) for i in (d.get('patents') or [])],
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            searchType=d.get('searchType'),
            searchUrl=d.get('searchUrl'),
            source=d.get('source'),
            trust=d.get('trust'),
        )

@dataclass
class ResearchExportResponse:
    document: Optional[str] = None
    exportedAt: Optional[str] = None
    format: Optional[str] = None
    researchGoal: Optional[str] = None
    sessionId: Optional[str] = None
    sourceCount: Optional[int] = None
    startedAt: Optional[str] = None
    stepCount: Optional[int] = None
    tenantId: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ResearchExportResponse | None":
        if d is None:
            return None
        return cls(
            document=d.get('document'),
            exportedAt=d.get('exportedAt'),
            format=d.get('format'),
            researchGoal=d.get('researchGoal'),
            sessionId=d.get('sessionId'),
            sourceCount=d.get('sourceCount'),
            startedAt=d.get('startedAt'),
            stepCount=d.get('stepCount'),
            tenantId=d.get('tenantId'),
            trust=d.get('trust'),
        )

@dataclass
class ScrapePageMetadata:
    author: Optional[str] = None
    title: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ScrapePageMetadata | None":
        if d is None:
            return None
        return cls(
            author=d.get('author'),
            title=d.get('title'),
        )

@dataclass
class ScrapePageResponse:
    authorityTier: Optional[str] = None
    citation: Optional[Citation] = None
    content: Optional[str] = None
    contentLength: Optional[int] = None
    contentType: Optional[str] = None
    detectedDoi: Optional[str] = None
    domainCategory: Optional[str] = None
    estimatedTokens: Optional[int] = None
    extractedBy: Optional[str] = None
    extractionQuality: Optional[str] = None
    metadata: Optional[ScrapePageMetadata] = None
    raw: Optional[bool] = None
    retractionStatus: Optional[Any] = None
    sizeCategory: Optional[str] = None
    sourceType: Optional[str] = None
    structuredData: Optional[StructuredData] = None
    truncated: Optional[bool] = None
    trust: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "ScrapePageResponse | None":
        if d is None:
            return None
        return cls(
            authorityTier=d.get('authorityTier'),
            citation=Citation.from_dict(d.get('citation')) if d.get('citation') else None,
            content=d.get('content'),
            contentLength=d.get('contentLength'),
            contentType=d.get('contentType'),
            detectedDoi=d.get('detectedDoi'),
            domainCategory=d.get('domainCategory'),
            estimatedTokens=d.get('estimatedTokens'),
            extractedBy=d.get('extractedBy'),
            extractionQuality=d.get('extractionQuality'),
            metadata=ScrapePageMetadata.from_dict(d.get('metadata')) if d.get('metadata') else None,
            raw=d.get('raw'),
            retractionStatus=d.get('retractionStatus') or None,
            sizeCategory=d.get('sizeCategory'),
            sourceType=d.get('sourceType'),
            structuredData=StructuredData.from_dict(d.get('structuredData')) if d.get('structuredData') else None,
            truncated=d.get('truncated'),
            trust=d.get('trust'),
            url=d.get('url'),
        )

@dataclass
class SearchAndScrapeComponent:
    autoFormatted: Optional[bool] = None
    card: dict[str, Any] = field(default_factory=dict)
    label: Optional[str] = None
    sourceRefs: list[str] = field(default_factory=list)
    table: dict[str, Any] = field(default_factory=dict)
    title: Optional[str] = None
    type: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SearchAndScrapeComponent | None":
        if d is None:
            return None
        return cls(
            autoFormatted=d.get('autoFormatted'),
            card=dict(d.get('card') or {}),
            label=d.get('label'),
            sourceRefs=list(d.get('sourceRefs') or []),
            table=dict(d.get('table') or {}),
            title=d.get('title'),
            type=d.get('type'),
        )

@dataclass
class SearchAndScrapeRecommendation:
    reason: Optional[str] = None
    score: Optional[float] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SearchAndScrapeRecommendation | None":
        if d is None:
            return None
        return cls(
            reason=d.get('reason'),
            score=d.get('score'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class SearchAndScrapeResponse:
    combinedContent: Optional[str] = None
    components: list[SearchAndScrapeComponent] = field(default_factory=list)
    note: Optional[str] = None
    query: Optional[str] = None
    recommendations: list[SearchAndScrapeRecommendation] = field(default_factory=list)
    scrapeFailures: list[SearchAndScrapeScrapefailure] = field(default_factory=list)
    sizeMetadata: Optional[SizeMetadata] = None
    sources: list[SearchAndScrapeSource] = field(default_factory=list)
    status: Optional[str] = None
    summary: Optional[Summary] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SearchAndScrapeResponse | None":
        if d is None:
            return None
        return cls(
            combinedContent=d.get('combinedContent'),
            components=[SearchAndScrapeComponent.from_dict(i) for i in (d.get('components') or [])],
            note=d.get('note'),
            query=d.get('query'),
            recommendations=[SearchAndScrapeRecommendation.from_dict(i) for i in (d.get('recommendations') or [])],
            scrapeFailures=[SearchAndScrapeScrapefailure.from_dict(i) for i in (d.get('scrapeFailures') or [])],
            sizeMetadata=SizeMetadata.from_dict(d.get('sizeMetadata')) if d.get('sizeMetadata') else None,
            sources=[SearchAndScrapeSource.from_dict(i) for i in (d.get('sources') or [])],
            status=d.get('status'),
            summary=Summary.from_dict(d.get('summary')) if d.get('summary') else None,
            trust=d.get('trust'),
        )

@dataclass
class SearchAndScrapeScrapefailure:
    kind: Optional[str] = None
    reason: Optional[str] = None
    retryable: Optional[bool] = None
    suggestedAction: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SearchAndScrapeScrapefailure | None":
        if d is None:
            return None
        return cls(
            kind=d.get('kind'),
            reason=d.get('reason'),
            retryable=d.get('retryable'),
            suggestedAction=d.get('suggestedAction'),
            url=d.get('url'),
        )

@dataclass
class SearchAndScrapeSource:
    authorityTier: Optional[str] = None
    claimSignal: Optional[str] = None
    content: Optional[str] = None
    contentType: Optional[str] = None
    domainCategory: Optional[str] = None
    keySentences: list[str] = field(default_factory=list)
    scores: dict[str, Any] = field(default_factory=dict)
    sourceType: Optional[str] = None
    title: Optional[str] = None
    trust: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SearchAndScrapeSource | None":
        if d is None:
            return None
        return cls(
            authorityTier=d.get('authorityTier'),
            claimSignal=d.get('claimSignal'),
            content=d.get('content'),
            contentType=d.get('contentType'),
            domainCategory=d.get('domainCategory'),
            keySentences=list(d.get('keySentences') or []),
            scores=dict(d.get('scores') or {}),
            sourceType=d.get('sourceType'),
            title=d.get('title'),
            trust=d.get('trust'),
            url=d.get('url'),
        )

@dataclass
class SequentialSearchRefinementresult:
    error: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    results: list[SequentialSearchResult] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SequentialSearchRefinementresult | None":
        if d is None:
            return None
        return cls(
            error=d.get('error'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            results=[SequentialSearchResult.from_dict(i) for i in (d.get('results') or [])],
        )

@dataclass
class SequentialSearchResponse:
    completedAt: Optional[str] = None
    coverage: Optional[Coverage] = None
    currentStep: Optional[int] = None
    depth: Optional[str] = None
    gaps: list[GetResearchSessionGap] = field(default_factory=list)
    isComplete: Optional[bool] = None
    lastSteps: list[GetResearchSessionLaststep] = field(default_factory=list)
    refinementNote: Optional[str] = None
    refinementQueries: list[str] = field(default_factory=list)
    refinementResults: list[SequentialSearchRefinementresult] = field(default_factory=list)
    researchGoal: Optional[str] = None
    responseMode: Optional[str] = None
    sessionId: Optional[str] = None
    sources: list[GetResearchSessionSource] = field(default_factory=list)
    startedAt: Optional[str] = None
    stepIndex: list[GetResearchSessionStepindex] = field(default_factory=list)
    steps: list[GetResearchSessionStepindex] = field(default_factory=list)
    summary: Optional[str] = None
    totalStepsEstimate: Optional[int] = None
    trust: Optional[str] = None
    warning: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SequentialSearchResponse | None":
        if d is None:
            return None
        return cls(
            completedAt=d.get('completedAt'),
            coverage=Coverage.from_dict(d.get('coverage')) if d.get('coverage') else None,
            currentStep=d.get('currentStep'),
            depth=d.get('depth'),
            gaps=[GetResearchSessionGap.from_dict(i) for i in (d.get('gaps') or [])],
            isComplete=d.get('isComplete'),
            lastSteps=[GetResearchSessionLaststep.from_dict(i) for i in (d.get('lastSteps') or [])],
            refinementNote=d.get('refinementNote'),
            refinementQueries=list(d.get('refinementQueries') or []),
            refinementResults=[SequentialSearchRefinementresult.from_dict(i) for i in (d.get('refinementResults') or [])],
            researchGoal=d.get('researchGoal'),
            responseMode=d.get('responseMode'),
            sessionId=d.get('sessionId'),
            sources=[GetResearchSessionSource.from_dict(i) for i in (d.get('sources') or [])],
            startedAt=d.get('startedAt'),
            stepIndex=[GetResearchSessionStepindex.from_dict(i) for i in (d.get('stepIndex') or [])],
            steps=[GetResearchSessionStepindex.from_dict(i) for i in (d.get('steps') or [])],
            summary=d.get('summary'),
            totalStepsEstimate=d.get('totalStepsEstimate'),
            trust=d.get('trust'),
            warning=d.get('warning'),
        )

@dataclass
class SequentialSearchResult:
    snippet: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SequentialSearchResult | None":
        if d is None:
            return None
        return cls(
            snippet=d.get('snippet'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class SizeMetadata:
    estimatedTokens: Optional[int] = None
    sizeCategory: Optional[str] = None
    totalLength: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "SizeMetadata | None":
        if d is None:
            return None
        return cls(
            estimatedTokens=d.get('estimatedTokens'),
            sizeCategory=d.get('sizeCategory'),
            totalLength=d.get('totalLength'),
        )

@dataclass
class StructuredData:
    citation: dict[str, Any] = field(default_factory=dict)
    jsonLd: list[Any] = field(default_factory=list)
    openGraph: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "StructuredData | None":
        if d is None:
            return None
        return cls(
            citation=dict(d.get('citation') or {}),
            jsonLd=list(d.get('jsonLd') or []),
            openGraph=dict(d.get('openGraph') or {}),
        )

@dataclass
class StructuredSearchResponse:
    category: Optional[str] = None
    costUsd: Optional[float] = None
    hints: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None
    query: Optional[str] = None
    resultCount: Optional[int] = None
    results: list[StructuredSearchResult] = field(default_factory=list)
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "StructuredSearchResponse | None":
        if d is None:
            return None
        return cls(
            category=d.get('category'),
            costUsd=d.get('costUsd'),
            hints=dict(d.get('hints') or {}),
            provider=d.get('provider'),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            results=[StructuredSearchResult.from_dict(i) for i in (d.get('results') or [])],
            trust=d.get('trust'),
        )

@dataclass
class StructuredSearchResult:
    author: Optional[str] = None
    entities: list[Any] = field(default_factory=list)
    highlights: list[str] = field(default_factory=list)
    publishedDate: Optional[str] = None
    summary: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "StructuredSearchResult | None":
        if d is None:
            return None
        return cls(
            author=d.get('author'),
            entities=list(d.get('entities') or []),
            highlights=list(d.get('highlights') or []),
            publishedDate=d.get('publishedDate'),
            summary=d.get('summary'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class Summary:
    processingTimeMs: Optional[int] = None
    urlsFailed: Optional[int] = None
    urlsScraped: Optional[int] = None
    urlsSearched: Optional[int] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "Summary | None":
        if d is None:
            return None
        return cls(
            processingTimeMs=d.get('processingTimeMs'),
            urlsFailed=d.get('urlsFailed'),
            urlsScraped=d.get('urlsScraped'),
            urlsSearched=d.get('urlsSearched'),
        )

@dataclass
class VerifyCitationResponse:
    archivedUrl: Optional[str] = None
    claim: Optional[str] = None
    claimEvidence: list[str] = field(default_factory=list)
    claimSourceUrl: Optional[str] = None
    claimSupport: Optional[str] = None
    conflictOfInterest: Optional[ConflictOfInterest] = None
    contrastSignal: Optional[bool] = None
    detectedDoi: Optional[str] = None
    exists: Optional[bool] = None
    httpStatus: Optional[int] = None
    input: Optional[str] = None
    inputType: Optional[str] = None
    matchConfidence: Optional[str] = None
    matchedRecord: Optional[Any] = None
    provenance: list[str] = field(default_factory=list)
    retractionStatus: Optional[RetractionStatus] = None
    titleMatch: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "VerifyCitationResponse | None":
        if d is None:
            return None
        return cls(
            archivedUrl=d.get('archivedUrl'),
            claim=d.get('claim'),
            claimEvidence=list(d.get('claimEvidence') or []),
            claimSourceUrl=d.get('claimSourceUrl'),
            claimSupport=d.get('claimSupport'),
            conflictOfInterest=ConflictOfInterest.from_dict(d.get('conflictOfInterest')) if d.get('conflictOfInterest') else None,
            contrastSignal=d.get('contrastSignal'),
            detectedDoi=d.get('detectedDoi'),
            exists=d.get('exists'),
            httpStatus=d.get('httpStatus'),
            input=d.get('input'),
            inputType=d.get('inputType'),
            matchConfidence=d.get('matchConfidence'),
            matchedRecord=d.get('matchedRecord') or None,
            provenance=list(d.get('provenance') or []),
            retractionStatus=RetractionStatus.from_dict(d.get('retractionStatus')) if d.get('retractionStatus') else None,
            titleMatch=d.get('titleMatch'),
            trust=d.get('trust'),
        )

@dataclass
class VerifyRecommendationConflictOfInterest:
    authorAffiliation: Optional[str] = None
    citedEntityName: Optional[str] = None
    confidence: Optional[str] = None
    conflictType: Optional[str] = None
    detected: Optional[bool] = None
    evidence: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "VerifyRecommendationConflictOfInterest | None":
        if d is None:
            return None
        return cls(
            authorAffiliation=d.get('authorAffiliation'),
            citedEntityName=d.get('citedEntityName'),
            confidence=d.get('confidence'),
            conflictType=d.get('conflictType'),
            detected=d.get('detected'),
            evidence=d.get('evidence'),
        )

@dataclass
class VerifyRecommendationRecommendation:
    author: Optional[str] = None
    conflictOfInterest: Optional[VerifyRecommendationConflictOfInterest] = None
    domainReputation: dict[str, Any] = field(default_factory=dict)
    flags: list[str] = field(default_factory=list)
    httpStatus: Optional[int] = None
    linkLive: Optional[bool] = None
    reasons: list[str] = field(default_factory=list)
    selfPromotionSignal: dict[str, Any] = field(default_factory=dict)
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "VerifyRecommendationRecommendation | None":
        if d is None:
            return None
        return cls(
            author=d.get('author'),
            conflictOfInterest=VerifyRecommendationConflictOfInterest.from_dict(d.get('conflictOfInterest')) if d.get('conflictOfInterest') else None,
            domainReputation=dict(d.get('domainReputation') or {}),
            flags=list(d.get('flags') or []),
            httpStatus=d.get('httpStatus'),
            linkLive=d.get('linkLive'),
            reasons=list(d.get('reasons') or []),
            selfPromotionSignal=dict(d.get('selfPromotionSignal') or {}),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class VerifyRecommendationResponse:
    itemCount: Optional[int] = None
    recommendations: list[VerifyRecommendationRecommendation] = field(default_factory=list)
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "VerifyRecommendationResponse | None":
        if d is None:
            return None
        return cls(
            itemCount=d.get('itemCount'),
            recommendations=[VerifyRecommendationRecommendation.from_dict(i) for i in (d.get('recommendations') or [])],
            trust=d.get('trust'),
        )

@dataclass
class WebSearchResponse:
    hints: dict[str, Any] = field(default_factory=dict)
    query: Optional[str] = None
    resultCount: Optional[int] = None
    results: list[WebSearchResult] = field(default_factory=list)
    trust: Optional[str] = None
    urls: list[str] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "WebSearchResponse | None":
        if d is None:
            return None
        return cls(
            hints=dict(d.get('hints') or {}),
            query=d.get('query'),
            resultCount=d.get('resultCount'),
            results=[WebSearchResult.from_dict(i) for i in (d.get('results') or [])],
            trust=d.get('trust'),
            urls=list(d.get('urls') or []),
        )

@dataclass
class WebSearchResult:
    claimSignal: Optional[str] = None
    displayLink: Optional[str] = None
    snippet: Optional[str] = None
    title: Optional[str] = None
    url: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "WebSearchResult | None":
        if d is None:
            return None
        return cls(
            claimSignal=d.get('claimSignal'),
            displayLink=d.get('displayLink'),
            snippet=d.get('snippet'),
            title=d.get('title'),
            url=d.get('url'),
        )

@dataclass
class WorkspaceContributeResponse:
    id: Optional[str] = None
    reason: Optional[str] = None
    status: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "WorkspaceContributeResponse | None":
        if d is None:
            return None
        return cls(
            id=d.get('id'),
            reason=d.get('reason'),
            status=d.get('status'),
        )

@dataclass
class WorkspaceReadContribution:
    contributorTenant: Optional[str] = None
    contributorUser: Optional[str] = None
    createdAt: Optional[str] = None
    id: Optional[str] = None
    note: Optional[str] = None
    tags: list[str] = field(default_factory=list)
    url: Optional[str] = None
    workspaceId: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "WorkspaceReadContribution | None":
        if d is None:
            return None
        return cls(
            contributorTenant=d.get('contributorTenant'),
            contributorUser=d.get('contributorUser'),
            createdAt=d.get('createdAt'),
            id=d.get('id'),
            note=d.get('note'),
            tags=list(d.get('tags') or []),
            url=d.get('url'),
            workspaceId=d.get('workspaceId'),
        )

@dataclass
class WorkspaceReadResponse:
    contributions: list[WorkspaceReadContribution] = field(default_factory=list)
    count: Optional[int] = None
    status: Optional[str] = None
    trust: Optional[str] = None

    @classmethod
    def from_dict(cls, d: dict[str, Any] | None) -> "WorkspaceReadResponse | None":
        if d is None:
            return None
        return cls(
            contributions=[WorkspaceReadContribution.from_dict(i) for i in (d.get('contributions') or [])],
            count=d.get('count'),
            status=d.get('status'),
            trust=d.get('trust'),
        )



# ---------------------------------------------------------------------------
# Backward-compatible aliases — old names used by existing tests/code.
# The canonical names are the ones above (generated from the Go output schema).
# ---------------------------------------------------------------------------

# MCPError repr — include code so repr(err) shows the error code.
MCPError.__repr__ = lambda self: f"MCPError({self.message!r}, code={self.code!r})"


# RetractionStatus — the server exposes retractionStatus as a plain dict; this
# typed helper lets callers opt in to structured access without schema churn.
@dataclass
class RetractionStatus:
    """Typed view of a Crossref retraction record (plain dict in the server schema)."""

    retracted: Optional[bool] = None
    kind: Optional[str] = None
    date: Optional[str] = None
    noticeDoi: Optional[str] = None
    source: Optional[str] = None

    @classmethod
    def from_dict(cls, d: "dict[str, Any] | None") -> "RetractionStatus | None":
        if not d:
            return None
        return cls(
            retracted=d.get("retracted"),
            kind=d.get("kind"),
            date=d.get("date"),
            noticeDoi=d.get("noticeDoi"),
            source=d.get("source"),
        )


# Root response aliases (canonical: {ToolName}Response)
SearchResponse = WebSearchResponse
ScrapeResult = ScrapePageResponse
ArchiveResult = ArchiveSourceResponse
AuditBibliographyResult = AuditBibliographyResponse
VerifyResult = VerifyCitationResponse
SearchAndScrapeResult = SearchAndScrapeResponse

# AuditBibliographySummary — the audit_bibliography.summary schema has different
# fields from search_and_scrape.summary; define it explicitly to avoid dedup collision.
@dataclass
class AuditBibliographySummary:
    """Corpus-level counts from audit_bibliography."""

    total: int = 0
    retracted: int = 0
    deadLink: int = 0
    notFound: int = 0
    unchecked: int = 0
    mischaracterized: int = 0
    ok: int = 0

    @classmethod
    def from_dict(cls, d: "dict[str, Any] | None") -> "AuditBibliographySummary":
        if not d:
            return cls()
        return cls(
            total=d.get("total") or 0,
            retracted=d.get("retracted") or 0,
            deadLink=d.get("deadLink") or 0,
            notFound=d.get("notFound") or 0,
            unchecked=d.get("unchecked") or 0,
            mischaracterized=d.get("mischaracterized") or 0,
            ok=d.get("ok") or 0,
        )


# Item / nested type aliases
SearchResult = WebSearchResult          # results[] item in web_search
AcademicPaper = AcademicSearchPaper     # papers[] item in academic_search
ImageResult = ImageSearchImage          # images[] item in image_search
NewsArticle = NewsSearchArticle         # articles[] item in news_search
BibEntryAudit = AuditBibliographyEntry  # entries[] item in audit_bibliography
AuditSummary = AuditBibliographySummary  # summary object in audit_bibliography
